package envd

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

const testSandboxID = "sbx-test-0001"

func bufconnServer(t *testing.T, executor Executor) (*Client, func()) {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer(grpc.ForceServerCodec(JSONCodec{}))
	RegisterEnvdServer(srv, &Server{SandboxID: testSandboxID, Executor: executor})

	go func() { _ = srv.Serve(lis) }()
	conn, err := grpc.DialContext(context.Background(), "bufconn",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(JSONCodec{})),
	)
	if err != nil {
		t.Fatalf("dial bufconn: %v", err)
	}
	return NewClient(conn), func() {
		_ = conn.Close()
		srv.Stop()
	}
}

func withAuth(ctx context.Context, sandboxID, scopes string) context.Context {
	return metadata.AppendToOutgoingContext(ctx,
		"x-kubebox-sandbox-id", sandboxID,
		"x-kubebox-scope", scopes,
	)
}

func withMetadata(ctx context.Context) context.Context {
	return withAuth(ctx, testSandboxID, "commands,files")
}

func missingScope(ctx context.Context) context.Context {
	return withAuth(ctx, testSandboxID, "files")
}

func wrongIdentity(ctx context.Context) context.Context {
	return withAuth(ctx, "sbx-evil", "commands")
}

func TestHealthNoAuth(t *testing.T) {
	client, closeFn := bufconnServer(t, NewMemoryExecutor())
	defer closeFn()
	resp, err := client.Health(context.Background())
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if resp.Status != "ok" {
		t.Fatalf("status = %q, want ok", resp.Status)
	}
}

func TestExecRequiresIdentity(t *testing.T) {
	client, closeFn := bufconnServer(t, NewMemoryExecutor())
	defer closeFn()
	_, err := client.Exec(context.Background(), &ExecRequest{Command: "ls"})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("err = %v, want Unauthenticated", err)
	}
}

func TestExecRejectsWrongIdentity(t *testing.T) {
	client, closeFn := bufconnServer(t, NewMemoryExecutor())
	defer closeFn()
	_, err := client.Exec(wrongIdentity(context.Background()), &ExecRequest{Command: "ls"})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("err = %v, want PermissionDenied", err)
	}
}

func TestExecRejectsMissingScope(t *testing.T) {
	client, closeFn := bufconnServer(t, NewMemoryExecutor())
	defer closeFn()
	_, err := client.Exec(missingScope(context.Background()), &ExecRequest{Command: "ls"})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("err = %v, want PermissionDenied", err)
	}
}

func TestExecAuthorized(t *testing.T) {
	client, closeFn := bufconnServer(t, NewMemoryExecutor())
	defer closeFn()
	resp, err := client.Exec(withMetadata(context.Background()), &ExecRequest{Command: "go test"})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if resp.ExitCode != 0 {
		t.Fatalf("exitCode = %d, want 0", resp.ExitCode)
	}
	if resp.Stdout == "" {
		t.Fatal("stdout is empty")
	}
}

func TestWriteReadFile(t *testing.T) {
	client, closeFn := bufconnServer(t, NewMemoryExecutor())
	defer closeFn()
	ctx := withMetadata(context.Background())
	if _, err := client.WriteFile(ctx, &WriteFileRequest{Path: "/tmp/a.txt", Data: []byte("hello")}); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	resp, err := client.ReadFile(ctx, &ReadFileRequest{Path: "/tmp/a.txt"})
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(resp.Data) != "hello" {
		t.Fatalf("data = %q, want hello", resp.Data)
	}
}

func TestWriteFileRejectsTraversal(t *testing.T) {
	client, closeFn := bufconnServer(t, NewMemoryExecutor())
	defer closeFn()
	_, err := client.WriteFile(withMetadata(context.Background()), &WriteFileRequest{Path: "../escape", Data: []byte("x")})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("err = %v, want InvalidArgument", err)
	}
}

func TestReadFileMissing(t *testing.T) {
	client, closeFn := bufconnServer(t, NewMemoryExecutor())
	defer closeFn()
	_, err := client.ReadFile(withMetadata(context.Background()), &ReadFileRequest{Path: "/nope"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("err = %v, want NotFound", err)
	}
}

func TestExecutorUnavailable(t *testing.T) {
	client, closeFn := bufconnServer(t, nil)
	defer closeFn()
	_, err := client.Exec(withMetadata(context.Background()), &ExecRequest{Command: "ls"})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("err = %v, want FailedPrecondition", err)
	}
}
