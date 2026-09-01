package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Migrate creates the governance tables for the selected SQL dialect.
// Production should prefer MigrateLocked so replicas do not race.
func (s *SQLStore) Migrate(ctx context.Context) error {
	for _, statement := range s.SchemaStatements() {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

// MigrateLocked runs the schema migration under a dialect-appropriate advisory
// lock (Postgres pg_advisory_lock / MySQL GET_LOCK) so multiple replicas do not
// race on first startup.
func (s *SQLStore) MigrateLocked(ctx context.Context) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	unlock, err := s.acquireMigrationLock(ctx, conn)
	if err != nil {
		return err
	}
	defer unlock()
	for _, statement := range s.SchemaStatements() {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

// acquireMigrationLock takes an advisory lock and returns a release function.
const migrationLockKey = 7378697629483820646 // stable, arbitrarily-chosen 64-bit key

func (s *SQLStore) acquireMigrationLock(ctx context.Context, conn *sql.Conn) (func(), error) {
	if s.dialect == DialectPostgres {
		if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, migrationLockKey); err != nil {
			return nil, fmt.Errorf("acquire migration lock: %w", err)
		}
		return func() {
			_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, migrationLockKey)
		}, nil
	}
	var got sql.NullInt64
	if err := conn.QueryRowContext(ctx, `SELECT GET_LOCK('kubebox_migrate', 30)`).Scan(&got); err != nil {
		return nil, fmt.Errorf("acquire migration lock: %w", err)
	}
	if !got.Valid || got.Int64 != 1 {
		return nil, errors.New("migration lock acquisition timed out")
	}
	return func() {
		_, _ = conn.ExecContext(context.Background(), `SELECT RELEASE_LOCK('kubebox_migrate')`)
	}, nil
}
