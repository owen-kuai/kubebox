package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Dialect string

const (
	DialectPostgres Dialect = "postgres"
	DialectMySQL    Dialect = "mysql"
)

type SQLStore struct {
	db      *sql.DB
	dialect Dialect
}

func NewSQLStore(db *sql.DB, dialect Dialect) (*SQLStore, error) {
	if db == nil {
		return nil, errors.New("database is required")
	}
	if dialect != DialectPostgres && dialect != DialectMySQL {
		return nil, fmt.Errorf("unsupported SQL dialect %q", dialect)
	}
	return &SQLStore{db: db, dialect: dialect}, nil
}

// SchemaStatements returns the governance tables required by the MVP.
// The caller should execute these statements with its migration framework.
func (s *SQLStore) SchemaStatements() []string {
	if s.dialect == DialectPostgres {
		return []string{
			`CREATE TABLE IF NOT EXISTS t_owner_quota (tenant_id VARCHAR(128) NOT NULL, owner_id VARCHAR(128) NOT NULL, concurrent_limit INTEGER NOT NULL, current_count INTEGER NOT NULL DEFAULT 0 CHECK (current_count >= 0), PRIMARY KEY (tenant_id, owner_id))`,
			`CREATE TABLE IF NOT EXISTS t_quota_allocation (allocation_id VARCHAR(128) PRIMARY KEY, tenant_id VARCHAR(128) NOT NULL, owner_id VARCHAR(128) NOT NULL, sandbox_id VARCHAR(128) NOT NULL, status VARCHAR(16) NOT NULL, created_time TIMESTAMP(3) NOT NULL, released_time TIMESTAMP(3))`,
			`CREATE TABLE IF NOT EXISTS t_idempotency_key (tenant_id VARCHAR(128) NOT NULL, idem_key VARCHAR(256) NOT NULL, owner_id VARCHAR(128) NOT NULL, request_hash CHAR(64) NOT NULL, status VARCHAR(16) NOT NULL, resource_id VARCHAR(128), lease_version BIGINT NOT NULL DEFAULT 1, expires_at TIMESTAMP(3) NOT NULL, PRIMARY KEY (tenant_id, idem_key))`,
		}
	}
	return []string{
		`CREATE TABLE IF NOT EXISTS t_owner_quota (tenant_id VARCHAR(128) NOT NULL, owner_id VARCHAR(128) NOT NULL, concurrent_limit INT NOT NULL, current_count INT NOT NULL DEFAULT 0, PRIMARY KEY (tenant_id, owner_id), CHECK (current_count >= 0))`,
		`CREATE TABLE IF NOT EXISTS t_quota_allocation (allocation_id VARCHAR(128) PRIMARY KEY, tenant_id VARCHAR(128) NOT NULL, owner_id VARCHAR(128) NOT NULL, sandbox_id VARCHAR(128) NOT NULL, status VARCHAR(16) NOT NULL, created_time DATETIME(3) NOT NULL, released_time DATETIME(3))`,
		`CREATE TABLE IF NOT EXISTS t_idempotency_key (tenant_id VARCHAR(128) NOT NULL, idem_key VARCHAR(256) NOT NULL, owner_id VARCHAR(128) NOT NULL, request_hash CHAR(64) NOT NULL, status VARCHAR(16) NOT NULL, resource_id VARCHAR(128), lease_version BIGINT NOT NULL DEFAULT 1, expires_at DATETIME(3) NOT NULL, PRIMARY KEY (tenant_id, idem_key))`,
	}
}

// SetQuota installs or raises the owner's concurrent limit. It refuses to
// lower the limit below the current in-use count.
func (s *SQLStore) SetQuota(ctx context.Context, tenantID, ownerID string, limit int) error {
	if tenantID == "" || ownerID == "" || limit < 0 {
		return ErrInvalidArgument
	}
	if s.dialect == DialectMySQL {
		query := `INSERT INTO t_owner_quota (tenant_id, owner_id, concurrent_limit, current_count) VALUES (?, ?, ?, 0) ON DUPLICATE KEY UPDATE concurrent_limit = IF(current_count > VALUES(concurrent_limit), current_count, VALUES(concurrent_limit))`
		if _, err := s.db.ExecContext(ctx, query, tenantID, ownerID, limit); err != nil {
			return err
		}
		return nil
	}
	// Postgres: upsert, then reject lowering below current usage.
	upsert := s.bind(`INSERT INTO t_owner_quota (tenant_id, owner_id, concurrent_limit, current_count) VALUES (?, ?, ?, 0) ON CONFLICT (tenant_id, owner_id) DO UPDATE SET concurrent_limit = EXCLUDED.concurrent_limit`)
	if _, err := s.db.ExecContext(ctx, upsert, tenantID, ownerID, limit); err != nil {
		return err
	}
	var current int
	check := s.bind(`SELECT current_count FROM t_owner_quota WHERE tenant_id = ? AND owner_id = ?`)
	if err := s.db.QueryRowContext(ctx, check, tenantID, ownerID).Scan(&current); err != nil {
		return err
	}
	if current > limit {
		restore := s.bind(`UPDATE t_owner_quota SET concurrent_limit = current_count WHERE tenant_id = ? AND owner_id = ?`)
		_, _ = s.db.ExecContext(ctx, restore, tenantID, ownerID)
		return ErrQuotaBelowUsage
	}
	return nil
}

// ReserveAllocation atomically reserves one owner quota slot and creates its
// allocation ledger row. The transaction commits both changes or neither.
func (s *SQLStore) ReserveAllocation(ctx context.Context, tenantID, ownerID, allocationID, sandboxID string, now time.Time) error {
	if tenantID == "" || ownerID == "" || allocationID == "" || sandboxID == "" {
		return ErrInvalidArgument
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	rollback := func(cause error) error { _ = tx.Rollback(); return cause }
	query := s.bind(`UPDATE t_owner_quota SET current_count = current_count + 1 WHERE tenant_id = ? AND owner_id = ? AND current_count < concurrent_limit`)
	result, err := tx.ExecContext(ctx, query, tenantID, ownerID)
	if err != nil {
		return rollback(err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return rollback(err)
	}
	if rows != 1 {
		return rollback(ErrQuotaExceeded)
	}
	insert := s.bind(`INSERT INTO t_quota_allocation (allocation_id, tenant_id, owner_id, sandbox_id, status, created_time) VALUES (?, ?, ?, ?, 'RESERVED', ?)`)
	if _, err := tx.ExecContext(ctx, insert, allocationID, tenantID, ownerID, sandboxID, now.UTC()); err != nil {
		return rollback(err)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

// ReleaseAllocation is idempotent: only the RESERVED -> RELEASED transition
// can decrement the aggregate quota, and both statements share one transaction.
func (s *SQLStore) ReleaseAllocation(ctx context.Context, tenantID, ownerID, allocationID string, now time.Time) error {
	if tenantID == "" || ownerID == "" || allocationID == "" {
		return ErrInvalidArgument
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	rollback := func(cause error) error { _ = tx.Rollback(); return cause }
	update := s.bind(`UPDATE t_quota_allocation SET status = 'RELEASED', released_time = ? WHERE allocation_id = ? AND tenant_id = ? AND owner_id = ? AND status = 'RESERVED'`)
	result, err := tx.ExecContext(ctx, update, now.UTC(), allocationID, tenantID, ownerID)
	if err != nil {
		return rollback(err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return rollback(err)
	}
	if rows == 0 {
		// Already released is a successful no-op. Missing allocation is an error.
		var status string
		lookup := s.bind(`SELECT status FROM t_quota_allocation WHERE allocation_id = ? AND tenant_id = ? AND owner_id = ?`)
		if err := tx.QueryRowContext(ctx, lookup, allocationID, tenantID, ownerID).Scan(&status); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return rollback(ErrAllocationNotFound)
			}
			return rollback(err)
		}
		if status == "RELEASED" {
			if err := tx.Commit(); err != nil {
				return err
			}
			return nil
		}
		return rollback(ErrAllocationState)
	}
	decrement := s.bind(`UPDATE t_owner_quota SET current_count = current_count - 1 WHERE tenant_id = ? AND owner_id = ? AND current_count > 0`)
	result, err = tx.ExecContext(ctx, decrement, tenantID, ownerID)
	if err != nil {
		return rollback(err)
	}
	rows, err = result.RowsAffected()
	if err != nil {
		return rollback(err)
	}
	if rows != 1 {
		return rollback(ErrQuotaCorrupt)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

// InsertIdempotencyPending claims a request fingerprint. A duplicate key is
// intentionally returned to the caller, which must compare request_hash and
// inspect the existing terminal/PENDING state before reusing the resource.
func (s *SQLStore) InsertIdempotencyPending(ctx context.Context, tenantID, ownerID, key, requestHash string, expiresAt time.Time) error {
	if tenantID == "" || ownerID == "" || key == "" || requestHash == "" {
		return ErrInvalidArgument
	}
	query := `INSERT INTO t_idempotency_key (tenant_id, idem_key, owner_id, request_hash, status, lease_version, expires_at) VALUES (?, ?, ?, ?, 'PENDING', 1, ?)`
	if s.dialect == DialectPostgres {
		query += ` ON CONFLICT (tenant_id, idem_key) DO NOTHING`
	} else {
		query += ` ON DUPLICATE KEY UPDATE idem_key = idem_key`
	}
	_, err := s.db.ExecContext(ctx, s.bind(query), tenantID, ownerID, key, requestHash, expiresAt.UTC())
	return err
}

func (s *SQLStore) bind(query string) string {
	if s.dialect != DialectPostgres {
		return query
	}
	var b strings.Builder
	index := 0
	for _, part := range strings.Split(query, "?") {
		if index > 0 {
			b.WriteString(fmt.Sprintf("$%d", index))
		}
		b.WriteString(part)
		index++
	}
	return b.String()
}

var (
	ErrInvalidArgument    = errors.New("invalid persistence argument")
	ErrQuotaExceeded      = errors.New("quota exceeded")
	ErrQuotaBelowUsage    = errors.New("quota below current usage")
	ErrQuotaNotConfigured = errors.New("quota not configured")
	ErrQuotaCorrupt       = errors.New("quota ledger is inconsistent")
	ErrAllocationNotFound = errors.New("allocation not found")
	ErrAllocationState    = errors.New("invalid allocation state")
)
