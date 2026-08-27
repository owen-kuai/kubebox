package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql" // registers "mysql" driver
	_ "github.com/jackc/pgx/v5/stdlib" // registers "pgx" driver
)

// OpenSQLStore opens a real PostgreSQL/MySQL connection from a URL-style DSN,
// verifies connectivity, runs the governance schema migration, and returns a
// store ready to serve as the durable GovernanceStore boundary.
//
// Supported schemes:
//
//	postgres://user:pass@host:port/db?sslmode=disable
//	postgresql://user:pass@host:port/db
//	mysql://user:pass@host:port/db
//
// The caller is responsible for closing the store (Close) on shutdown.
func OpenSQLStore(ctx context.Context, dsn string) (*SQLStore, error) {
	driverName, dialect, normalized, err := parseDSN(dsn)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open(driverName, normalized)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", dialect, err)
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(time.Hour)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping %s: %w", dialect, err)
	}
	store, err := NewSQLStore(db, dialect)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.MigrateLocked(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate %s: %w", dialect, err)
	}
	return store, nil
}

// parseDSN normalizes a URL-style DSN into a database/sql driver name and the
// driver's native DSN. PostgreSQL URLs pass through to pgx unchanged; MySQL
// URLs are rewritten to the go-sql-driver "user:pass@tcp(host:port)/db" form.
func parseDSN(dsn string) (driverName string, dialect Dialect, normalized string, err error) {
	lower := strings.ToLower(dsn)
	switch {
	case strings.HasPrefix(lower, "postgres://"), strings.HasPrefix(lower, "postgresql://"):
		return "pgx", DialectPostgres, dsn, nil
	case strings.HasPrefix(lower, "mysql://"):
		u, perr := url.Parse(dsn)
		if perr != nil {
			return "", "", "", fmt.Errorf("parse mysql DSN: %w", perr)
		}
		user := u.User.Username()
		pass, _ := u.User.Password()
		host := u.Host
		dbname := strings.TrimPrefix(u.Path, "/")
		if host == "" || dbname == "" {
			return "", "", "", errors.New("mysql DSN requires a host and database name")
		}
		normalized = fmt.Sprintf("%s:%s@tcp(%s)/%s", user, pass, host, dbname)
		// Preserve any query params (e.g. tls=...) and force time.Time scanning.
		query := u.Query()
		query.Set("parseTime", "true")
		if encoded := query.Encode(); encoded != "" {
			normalized += "?" + encoded
		}
		return "mysql", DialectMySQL, normalized, nil
	default:
		return "", "", "", fmt.Errorf("unsupported DSN scheme %q: use postgres:// or mysql://", dsn)
	}
}

// Close releases the underlying connection pool.
func (s *SQLStore) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}
