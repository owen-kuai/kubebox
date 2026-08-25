package envd

import (
	"context"
	"strings"
	"testing"
)

// TestGrpcProcessExecutor verifies the full gRPC path (auth + JSON codec +
// real process executor) for command and file operations.
func TestGrpcProcessExecutor(t *testing.T) {
	ex, err := NewProcessExecutor(t.TempDir())
	if err != nil {
		t.Fatalf("NewProcessExecutor: %v", err)
	}
	client, closeFn := bufconnServer(t, ex)
	defer closeFn()
	ctx := withMetadata(context.Background())

	resp, err := client.Exec(ctx, &ExecRequest{Command: "printf 'via-grpc\n'"})
	if err != nil {
		t.Fatalf("Exec via gRPC: %v", err)
	}
	if resp.ExitCode != 0 {
		t.Errorf("exitCode = %d, want 0", resp.ExitCode)
	}
	if !strings.Contains(resp.Stdout, "via-grpc") {
		t.Errorf("stdout = %q, want via-grpc", resp.Stdout)
	}

	if _, err := client.WriteFile(ctx, &WriteFileRequest{Path: "/data/note.txt", Data: []byte("x")}); err != nil {
		t.Fatalf("WriteFile via gRPC: %v", err)
	}
	got, err := client.ReadFile(ctx, &ReadFileRequest{Path: "/data/note.txt"})
	if err != nil {
		t.Fatalf("ReadFile via gRPC: %v", err)
	}
	if string(got.Data) != "x" {
		t.Errorf("data = %q, want x", got.Data)
	}
}
