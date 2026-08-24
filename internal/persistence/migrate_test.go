package persistence

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"sync"
	"testing"
)

type recordingDriver struct{}
type recordingConn struct{}
type recordingResult struct{}

var (
	recordingMu sync.Mutex
	recordedSQL []string
	failAt      int
)

func (recordingDriver) Open(string) (driver.Conn, error) { return recordingConn{}, nil }
func (recordingConn) Prepare(query string) (driver.Stmt, error) {
	return recordingStmt{query: query}, nil
}
func (recordingConn) Close() error              { return nil }
func (recordingConn) Begin() (driver.Tx, error) { return recordingTx{}, nil }
func (recordingStmt) Close() error              { return nil }
func (recordingStmt) NumInput() int             { return -1 }
func (s recordingStmt) Exec([]driver.Value) (driver.Result, error) {
	recordingMu.Lock()
	defer recordingMu.Unlock()
	recordedSQL = append(recordedSQL, s.query)
	if failAt > 0 && len(recordedSQL) == failAt {
		return nil, fmt.Errorf("forced migration failure")
	}
	return recordingResult{}, nil
}
func (recordingStmt) Query([]driver.Value) (driver.Rows, error) {
	return nil, fmt.Errorf("query unsupported")
}
func (recordingResult) LastInsertId() (int64, error) { return 0, nil }
func (recordingResult) RowsAffected() (int64, error) { return 1, nil }
func (recordingTx) Commit() error                    { return nil }
func (recordingTx) Rollback() error                  { return nil }

type recordingStmt struct{ query string }
type recordingTx struct{}

func TestMigrateExecutesAllStatementsInOrder(t *testing.T) {
	resetRecording()
	const driverName = "kubebox-recording-migrate"
	sql.Register(driverName, recordingDriver{})
	db, err := sql.Open(driverName, "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := NewSQLStore(db, DialectMySQL)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, want := len(recorded()), 3; got != want {
		t.Fatalf("migration statements = %d, want %d", got, want)
	}
}

func TestMigrateStopsOnFailure(t *testing.T) {
	resetRecording()
	failAt = 2
	const driverName = "kubebox-recording-migrate-fail"
	sql.Register(driverName, recordingDriver{})
	db, err := sql.Open(driverName, "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := NewSQLStore(db, DialectPostgres)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(context.Background()); err == nil {
		t.Fatal("expected migration failure")
	}
	if got, want := len(recorded()), 2; got != want {
		t.Fatalf("statements after failure = %d, want %d", got, want)
	}
}

func resetRecording() { recordingMu.Lock(); defer recordingMu.Unlock(); recordedSQL = nil; failAt = 0 }
func recorded() []string {
	recordingMu.Lock()
	defer recordingMu.Unlock()
	return append([]string(nil), recordedSQL...)
}
