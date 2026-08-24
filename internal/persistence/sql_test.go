package persistence

import (
	"strings"
	"testing"
)

func TestSchemaStatementsSupportBothDialects(t *testing.T) {
	for _, dialect := range []Dialect{DialectPostgres, DialectMySQL} {
		store := &SQLStore{dialect: dialect}
		statements := store.SchemaStatements()
		if len(statements) != 3 {
			t.Fatalf("%s statements = %d", dialect, len(statements))
		}
		joined := strings.Join(statements, "\n")
		for _, table := range []string{"t_owner_quota", "t_quota_allocation", "t_idempotency_key"} {
			if !strings.Contains(joined, table) {
				t.Fatalf("%s schema missing %s", dialect, table)
			}
		}
		if dialect == DialectPostgres && !strings.Contains(joined, "CHECK (current_count >= 0)") {
			t.Fatal("postgres schema missing non-negative quota constraint")
		}
	}
}

func TestBindPostgresPlaceholders(t *testing.T) {
	store := &SQLStore{dialect: DialectPostgres}
	got := store.bind("UPDATE t_owner_quota SET current_count = current_count + 1 WHERE tenant_id = ? AND owner_id = ? AND current_count < concurrent_limit")
	want := "UPDATE t_owner_quota SET current_count = current_count + 1 WHERE tenant_id = $1 AND owner_id = $2 AND current_count < concurrent_limit"
	if got != want {
		t.Fatalf("bind = %q, want %q", got, want)
	}
}

func TestBindMySQLPlaceholders(t *testing.T) {
	store := &SQLStore{dialect: DialectMySQL}
	query := "INSERT INTO t_quota_allocation (allocation_id, tenant_id) VALUES (?, ?)"
	if got := store.bind(query); got != query {
		t.Fatalf("mysql bind changed query: %q", got)
	}
}

func TestIdempotencyDialectClauses(t *testing.T) {
	for _, dialect := range []Dialect{DialectPostgres, DialectMySQL} {
		store := &SQLStore{dialect: dialect}
		query := `INSERT INTO t_idempotency_key (tenant_id, idem_key, owner_id, request_hash, status, lease_version, expires_at) VALUES (?, ?, ?, ?, 'PENDING', 1, ?)`
		if dialect == DialectPostgres {
			query += ` ON CONFLICT (tenant_id, idem_key) DO NOTHING`
		} else {
			query += ` ON DUPLICATE KEY UPDATE idem_key = idem_key`
		}
		bound := store.bind(query)
		if dialect == DialectPostgres && !strings.Contains(bound, "$5") {
			t.Fatalf("postgres query missing fifth placeholder: %s", bound)
		}
		if dialect == DialectMySQL && !strings.Contains(bound, "VALUES (?, ?, ?, ?, 'PENDING', 1, ?)") {
			t.Fatalf("mysql query changed unexpectedly: %s", bound)
		}
	}
}
