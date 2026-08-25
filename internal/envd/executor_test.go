package envd

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testExecutor(t *testing.T) *ProcessExecutor {
	t.Helper()
	root := t.TempDir()
	ex, err := NewProcessExecutor(root)
	if err != nil {
		t.Fatalf("NewProcessExecutor: %v", err)
	}
	return ex
}

func TestProcessExecOk(t *testing.T) {
	ex := testExecutor(t)
	ctx := context.Background()
	resp, err := ex.Exec(ctx, "printf hello")
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if resp.ExitCode != 0 {
		t.Errorf("exitCode = %d, want 0", resp.ExitCode)
	}
	if !strings.Contains(resp.Stdout, "hello") {
		t.Errorf("stdout = %q, want to contain hello", resp.Stdout)
	}
}

func TestProcessExecExitCode(t *testing.T) {
	ex := testExecutor(t)
	resp, err := ex.Exec(context.Background(), "exit 42")
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if resp.ExitCode != 42 {
		t.Errorf("exitCode = %d, want 42", resp.ExitCode)
	}
}

func TestProcessExecEmptyCommand(t *testing.T) {
	ex := testExecutor(t)
	_, err := ex.Exec(context.Background(), "   ")
	if !errors.Is(err, errEmptyCommand) {
		t.Fatalf("err = %v, want errEmptyCommand", err)
	}
}

func TestProcessExecTimeout(t *testing.T) {
	ex := testExecutor(t)
	ex.Timeout = 200 * time.Millisecond
	start := time.Now()
	_, err := ex.Exec(context.Background(), "sleep 5")
	if !errors.Is(err, errTimeout) {
		t.Fatalf("err = %v, want errTimeout (took %v)", err, time.Since(start))
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("exec did not respect timeout, took %v", time.Since(start))
	}
}

func TestProcessExecOutputCap(t *testing.T) {
	ex := testExecutor(t)
	ex.MaxOutput = 64
	resp, err := ex.Exec(context.Background(), "yes x | head -c 100000")
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if int64(len(resp.Stdout)) > ex.MaxOutput {
		t.Errorf("stdout len = %d, exceeds cap %d", len(resp.Stdout), ex.MaxOutput)
	}
}

func TestProcessWriteReadFile(t *testing.T) {
	ex := testExecutor(t)
	ctx := context.Background()
	if _, err := ex.WriteFile(ctx, "/tmp/nested/a.txt", []byte("hello")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	data, err := ex.ReadFile(ctx, "/tmp/nested/a.txt")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("data = %q, want hello", data)
	}
	// The file must physically live under root.
	full := filepath.Join(ex.Root, "tmp/nested/a.txt")
	if _, err := os.Stat(full); err != nil {
		t.Errorf("file not under root: %v", err)
	}
}

func TestProcessReadMissing(t *testing.T) {
	ex := testExecutor(t)
	_, err := ex.ReadFile(context.Background(), "/nope")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want fs.ErrNotExist", err)
	}
}

func TestProcessRejectPathTraversal(t *testing.T) {
	ex := testExecutor(t)
	ctx := context.Background()
	for _, p := range []string{"../../etc/passwd", "/../../etc/passwd", "a/../../../x"} {
		if _, err := ex.WriteFile(ctx, p, []byte("x")); !errors.Is(err, fs.ErrPermission) {
			t.Errorf("path %q: err = %v, want fs.ErrPermission", p, err)
		}
	}
}

func TestProcessRejectFileTooLarge(t *testing.T) {
	ex := testExecutor(t)
	ex.MaxFileLen = 4
	_, err := ex.WriteFile(context.Background(), "/big.txt", []byte("hello world"))
	if !errors.Is(err, errFileTooLarge) {
		t.Fatalf("err = %v, want errFileTooLarge", err)
	}
}

func TestProcessRejectSymlinkEscape(t *testing.T) {
	ex := testExecutor(t)
	// Create a real file outside root and a symlink inside root pointing to it.
	outside := filepath.Join(filepath.Dir(ex.Root), "outside-secret.txt")
	if err := os.WriteFile(outside, []byte("sensitive"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(ex.Root, "link.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unsupported on this fs: %v", err)
	}
	_, err := ex.ReadFile(context.Background(), "/link.txt")
	if err == nil {
		t.Fatal("read via escaping symlink should fail")
	}
}

func TestProcessWriteNotExistParentIsCreated(t *testing.T) {
	ex := testExecutor(t)
	ctx := context.Background()
	// First write creates the whole chain (root itself exists from TempDir).
	if _, err := ex.WriteFile(ctx, "deep/nonexistent/dir/f.txt", []byte("x")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ex.Root, "deep/nonexistent/dir/f.txt")); err != nil {
		t.Errorf("chain not created: %v", err)
	}
}
