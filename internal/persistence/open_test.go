package persistence

import (
	"strings"
	"testing"
)

func TestParseDSNPostgres(t *testing.T) {
	for _, dsn := range []string{
		"postgres://user:pass@db:5432/kubebox?sslmode=disable",
		"postgresql://user:pass@db:5432/kubebox",
	} {
		driver, dialect, normalized, err := parseDSN(dsn)
		if err != nil {
			t.Fatalf("parseDSN(%q): %v", dsn, err)
		}
		if driver != "pgx" || dialect != DialectPostgres {
			t.Fatalf("parseDSN(%q) = driver %q dialect %q", dsn, driver, dialect)
		}
		if normalized != dsn {
			t.Fatalf("parseDSN(%q) normalized = %q", dsn, normalized)
		}
	}
}

func TestParseDSNMySQL(t *testing.T) {
	driver, dialect, normalized, err := parseDSN("mysql://user:pass@db:3306/kubebox")
	if err != nil {
		t.Fatal(err)
	}
	if driver != "mysql" || dialect != DialectMySQL {
		t.Fatalf("driver %q dialect %q", driver, dialect)
	}
	if want := "user:pass@tcp(db:3306)/kubebox?parseTime=true"; normalized != want {
		t.Fatalf("normalized = %q, want %q", normalized, want)
	}
}

func TestParseDSNMySQLPreservesParams(t *testing.T) {
	_, _, normalized, err := parseDSN("mysql://user@db:3306/kubebox?tls=preferred&timeout=5s")
	if err != nil {
		t.Fatal(err)
	}
	// parseTime=true is always injected; user params are preserved.
	if !containsAll(normalized, "tls=preferred", "timeout=5s", "parseTime=true") {
		t.Fatalf("normalized = %q", normalized)
	}
}

func TestParseDSNRejectsUnsupported(t *testing.T) {
	if _, _, _, err := parseDSN("sqlite:///tmp/x.db"); err == nil {
		t.Fatal("expected unsupported scheme error")
	}
	if _, _, _, err := parseDSN("mysql://user@db:3306"); err == nil {
		t.Fatal("expected missing database name error")
	}
}

func containsAll(s string, wants ...string) bool {
	for _, w := range wants {
		if !strings.Contains(s, w) {
			return false
		}
	}
	return true
}
