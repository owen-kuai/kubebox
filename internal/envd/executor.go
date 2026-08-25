package envd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ProcessExecutor runs commands and does file IO inside a single sandbox root
// directory. Production wiring starts it as the main process of the isolated
// envd container (gVisor/Kata), so the host filesystem is never touched.
//
// Safety model:
//   - All file paths are resolved against root and must stay inside it.
//   - Symlinks that resolve outside root are rejected.
//   - Exec runs with a bounded timeout and caps stdout/stderr, so a runaway or
//     chatty command cannot exhaust memory.
type ProcessExecutor struct {
	Root       string
	WorkDir    string
	Timeout    time.Duration
	MaxOutput  int64
	MaxFileLen int64
}

const (
	defaultTimeout    = 30 * time.Second
	defaultMaxOutput  = 1 << 20 // 1 MiB
	defaultMaxFileLen = 8 << 20 // 8 MiB
)

// NewProcessExecutor builds a ProcessExecutor rooted at root. Root is resolved
// to an absolute, canonical path (symlinks evaluated if possible) so that later
// containment checks compare against the same canonical basis.
func NewProcessExecutor(root string) (*ProcessExecutor, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve root: %w", err)
	}
	abs = filepath.Clean(abs)
	// Best-effort canonicalization. If root does not exist yet, keep Abs and
	// let containment use the absolute path (which is fine when no symlink is
	// involved in the root prefix).
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return &ProcessExecutor{
		Root:       abs,
		WorkDir:    abs,
		Timeout:    defaultTimeout,
		MaxOutput:  defaultMaxOutput,
		MaxFileLen: defaultMaxFileLen,
	}, nil
}

var _ Executor = (*ProcessExecutor)(nil)

// Exec runs command through the system shell inside the sandbox workdir.
func (e *ProcessExecutor) Exec(ctx context.Context, command string) (ExecResponse, error) {
	if strings.TrimSpace(command) == "" {
		return ExecResponse{}, errEmptyCommand
	}
	timeout := e.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, "/bin/sh", "-c", command)
	cmd.Dir = e.WorkDir
	// Minimal, deterministic env; never inherit the host environment.
	cmd.Env = []string{
		"PATH=/usr/local/bin:/usr/bin:/bin",
		"HOME=" + e.WorkDir,
		"LANG=C.UTF-8",
	}

	maxOut := e.MaxOutput
	if maxOut <= 0 {
		maxOut = defaultMaxOutput
	}
	var stdout, stderr bytes.Buffer
	stdout.Grow(0)
	cmd.Stdout = &limitedWriter{w: &stdout, max: maxOut}
	cmd.Stderr = &limitedWriter{w: &stderr, max: maxOut}

	err := cmd.Run()
	resp := ExecResponse{Stdout: stdout.String(), Stderr: stderr.String()}
	if err != nil {
		if ctx.Err() != nil || cctx.Err() == context.DeadlineExceeded {
			return resp, errTimeout
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			resp.ExitCode = int32(ee.ExitCode())
			return resp, nil
		}
		return resp, fmt.Errorf("run command: %w", err)
	}
	return resp, nil
}

// ReadFile reads a file inside the sandbox root.
func (e *ProcessExecutor) ReadFile(_ context.Context, path string) ([]byte, error) {
	p, err := e.safePath(path)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fs.ErrNotExist
		}
		return nil, err
	}
	if fi.IsDir() {
		return nil, fs.ErrInvalid
	}
	maxLen := e.MaxFileLen
	if maxLen <= 0 {
		maxLen = defaultMaxFileLen
	}
	if fi.Size() > maxLen {
		return nil, errFileTooLarge
	}
	return os.ReadFile(p)
}

// WriteFile writes a file inside the sandbox root, creating parent dirs.
func (e *ProcessExecutor) WriteFile(_ context.Context, path string, data []byte) (int64, error) {
	maxLen := e.MaxFileLen
	if maxLen <= 0 {
		maxLen = defaultMaxFileLen
	}
	if int64(len(data)) > maxLen {
		return 0, errFileTooLarge
	}
	p, err := e.safePath(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Parent may not exist yet; safePath returns NotExist only when
			// the whole chain is missing and we are creating. Re-derive the
			// parent by resolving upward.
			p, err = e.createPath(path)
			if err != nil {
				return 0, err
			}
		} else {
			return 0, err
		}
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return 0, fmt.Errorf("create parent dirs: %w", err)
	}
	if err := os.WriteFile(p, data, 0o640); err != nil {
		return 0, fmt.Errorf("write file: %w", err)
	}
	return int64(len(data)), nil
}

// safePath resolves an in-sandbox path against Root and guarantees containment.
// For a not-yet-existing target it returns fs.ErrNotExist so callers that
// create can derive a writable path instead.
func (e *ProcessExecutor) safePath(path string) (string, error) {
	if path == "" {
		return "", fs.ErrInvalid
	}
	trimmed := path
	if filepath.IsAbs(trimmed) {
		trimmed = strings.TrimLeft(trimmed, string(filepath.Separator))
	}
	joined := filepath.Join(e.Root, trimmed)
	cleaned := filepath.Clean(joined)
	if !withinRoot(e.Root, cleaned) {
		return "", fs.ErrPermission
	}
	resolved, err := evalWithinRoot(e.Root, cleaned)
	if err != nil {
		return "", err
	}
	if !withinRoot(e.Root, resolved) {
		return "", fs.ErrPermission
	}
	return cleaned, nil
}

// createPath is used when writing to a brand-new location: it resolves the
// deepest existing ancestor to block a symlink-escape in the parent chain, then
// returns the full target path.
func (e *ProcessExecutor) createPath(path string) (string, error) {
	if path == "" {
		return "", fs.ErrInvalid
	}
	trimmed := path
	if filepath.IsAbs(trimmed) {
		trimmed = strings.TrimLeft(trimmed, string(filepath.Separator))
	}
	cleaned := filepath.Clean(filepath.Join(e.Root, trimmed))
	if !withinRoot(e.Root, cleaned) {
		return "", fs.ErrPermission
	}
	// Walk up until an existing path is found, resolve it, re-append tail.
	cur := cleaned
	var tail []string
	for {
		_, lerr := os.Lstat(cur)
		if lerr == nil {
			resolved, err := filepath.EvalSymlinks(cur)
			if err != nil {
				return "", err
			}
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			resolved = filepath.Clean(resolved)
			if !withinRoot(e.Root, resolved) {
				return "", fs.ErrPermission
			}
			return cleaned, nil
		}
		if os.IsNotExist(lerr) {
			parent := filepath.Dir(cur)
			if parent == cur {
				return "", fs.ErrNotExist
			}
			tail = append(tail, filepath.Base(cur))
			cur = parent
			continue
		}
		return "", lerr
	}
}

// evalWithinRoot resolves the deepest existing ancestor of target and returns
// the equivalent cleaned path, preserving any not-yet-existing tail.
func evalWithinRoot(root, target string) (string, error) {
	cur := target
	var tail []string
	for {
		_, lerr := os.Lstat(cur)
		if lerr == nil {
			resolved, err := filepath.EvalSymlinks(cur)
			if err != nil {
				return "", err
			}
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			return filepath.Clean(resolved), nil
		}
		if os.IsNotExist(lerr) {
			parent := filepath.Dir(cur)
			if parent == cur {
				return "", fs.ErrNotExist
			}
			tail = append(tail, filepath.Base(cur))
			cur = parent
			continue
		}
		return "", lerr
	}
}

func withinRoot(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// limitedWriter caps writes so a chatty command cannot blow memory.
type limitedWriter struct {
	w   *bytes.Buffer
	max int64
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	current := int64(l.w.Len())
	room := l.max - current
	if room <= 0 {
		return len(p), nil // discard silently
	}
	if int64(len(p)) > room {
		_, _ = l.w.Write(p[:room])
		return len(p), nil
	}
	_, _ = l.w.Write(p)
	return len(p), nil
}

var (
	errEmptyCommand = errors.New("command is required")
	errTimeout      = errors.New("command timed out")
	errFileTooLarge = errors.New("file exceeds max size")

	_ io.Writer = (*limitedWriter)(nil)
)
