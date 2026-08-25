package envd

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"strings"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/encoding"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const serviceName = "kubebox.envd.v1.Envd"

// JSONCodec keeps the first protocol slice runnable without protoc. The service
// boundary is still standard gRPC and can later switch to generated protobuf.
type JSONCodec struct{}

func (JSONCodec) Name() string                           { return "json" }
func (JSONCodec) Marshal(value any) ([]byte, error)      { return json.Marshal(value) }
func (JSONCodec) Unmarshal(data []byte, value any) error { return json.Unmarshal(data, value) }

func init() { encoding.RegisterCodec(JSONCodec{}) }

type HealthRequest struct{}
type HealthResponse struct {
	Status string `json:"status"`
}
type ExecRequest struct {
	Command string `json:"command"`
}
type ExecResponse struct {
	ExitCode int32  `json:"exitCode"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}
type ReadFileRequest struct {
	Path string `json:"path"`
}
type ReadFileResponse struct {
	Data []byte `json:"data"`
}
type WriteFileRequest struct {
	Path string `json:"path"`
	Data []byte `json:"data"`
}
type WriteFileResponse struct {
	BytesWritten int64 `json:"bytesWritten"`
}

type Executor interface {
	Exec(ctx context.Context, command string) (ExecResponse, error)
	ReadFile(ctx context.Context, path string) ([]byte, error)
	WriteFile(ctx context.Context, path string, data []byte) (int64, error)
}

type Server struct {
	UnimplementedEnvdServer
	SandboxID string
	Executor  Executor
}

func (s *Server) Health(context.Context, *HealthRequest) (*HealthResponse, error) {
	return &HealthResponse{Status: "ok"}, nil
}
func (s *Server) Exec(ctx context.Context, req *ExecRequest) (*ExecResponse, error) {
	if err := s.authorize(ctx, "commands"); err != nil {
		return nil, err
	}
	if s.Executor == nil {
		return nil, status.Error(codes.FailedPrecondition, "executor unavailable")
	}
	if strings.TrimSpace(req.Command) == "" {
		return nil, status.Error(codes.InvalidArgument, "command is required")
	}
	response, err := s.Executor.Exec(ctx, req.Command)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &response, nil
}
func (s *Server) ReadFile(ctx context.Context, req *ReadFileRequest) (*ReadFileResponse, error) {
	if err := s.authorize(ctx, "files"); err != nil {
		return nil, err
	}
	if s.Executor == nil {
		return nil, status.Error(codes.FailedPrecondition, "executor unavailable")
	}
	data, err := s.Executor.ReadFile(ctx, req.Path)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	return &ReadFileResponse{Data: data}, nil
}
func (s *Server) WriteFile(ctx context.Context, req *WriteFileRequest) (*WriteFileResponse, error) {
	if err := s.authorize(ctx, "files"); err != nil {
		return nil, err
	}
	if s.Executor == nil {
		return nil, status.Error(codes.FailedPrecondition, "executor unavailable")
	}
	written, err := s.Executor.WriteFile(ctx, req.Path, req.Data)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return &WriteFileResponse{BytesWritten: written}, nil
}

func (s *Server) authorize(ctx context.Context, scope string) error {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok || len(md.Get("x-kubebox-sandbox-id")) == 0 {
		return status.Error(codes.Unauthenticated, "internal sandbox identity is required")
	}
	if md.Get("x-kubebox-sandbox-id")[0] != s.SandboxID {
		return status.Error(codes.PermissionDenied, "sandbox identity rejected")
	}
	scopes := md.Get("x-kubebox-scope")
	for _, value := range scopes {
		for _, granted := range strings.Split(value, ",") {
			if strings.TrimSpace(granted) == scope {
				return nil
			}
		}
	}
	return status.Error(codes.PermissionDenied, "scope rejected")
}

type UnimplementedEnvdServer struct{}

func (UnimplementedEnvdServer) Health(context.Context, *HealthRequest) (*HealthResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method Health not implemented")
}
func (UnimplementedEnvdServer) Exec(context.Context, *ExecRequest) (*ExecResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method Exec not implemented")
}
func (UnimplementedEnvdServer) ReadFile(context.Context, *ReadFileRequest) (*ReadFileResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method ReadFile not implemented")
}
func (UnimplementedEnvdServer) WriteFile(context.Context, *WriteFileRequest) (*WriteFileResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method WriteFile not implemented")
}

type EnvdServer interface {
	Health(context.Context, *HealthRequest) (*HealthResponse, error)
	Exec(context.Context, *ExecRequest) (*ExecResponse, error)
	ReadFile(context.Context, *ReadFileRequest) (*ReadFileResponse, error)
	WriteFile(context.Context, *WriteFileRequest) (*WriteFileResponse, error)
}

func RegisterEnvdServer(server grpc.ServiceRegistrar, implementation EnvdServer) {
	server.RegisterService(&Envd_ServiceDesc, implementation)
}

var Envd_ServiceDesc = grpc.ServiceDesc{
	ServiceName: serviceName,
	HandlerType: (*EnvdServer)(nil),
	Methods: []grpc.MethodDesc{
		{MethodName: "Health", Handler: healthHandler},
		{MethodName: "Exec", Handler: execHandler},
		{MethodName: "ReadFile", Handler: readFileHandler},
		{MethodName: "WriteFile", Handler: writeFileHandler},
	},
}

func healthHandler(srv any, ctx context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
	in := new(HealthRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	return srv.(EnvdServer).Health(ctx, in)
}
func execHandler(srv any, ctx context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
	in := new(ExecRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	return srv.(EnvdServer).Exec(ctx, in)
}
func readFileHandler(srv any, ctx context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
	in := new(ReadFileRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	return srv.(EnvdServer).ReadFile(ctx, in)
}
func writeFileHandler(srv any, ctx context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
	in := new(WriteFileRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	return srv.(EnvdServer).WriteFile(ctx, in)
}

type Client struct{ conn *grpc.ClientConn }

func NewClient(conn *grpc.ClientConn) *Client { return &Client{conn: conn} }
func (c *Client) Health(ctx context.Context) (*HealthResponse, error) {
	out := new(HealthResponse)
	err := c.conn.Invoke(ctx, "/"+serviceName+"/Health", &HealthRequest{}, out, grpc.ForceCodec(JSONCodec{}))
	return out, err
}
func (c *Client) Exec(ctx context.Context, req *ExecRequest) (*ExecResponse, error) {
	out := new(ExecResponse)
	err := c.conn.Invoke(ctx, "/"+serviceName+"/Exec", req, out, grpc.ForceCodec(JSONCodec{}))
	return out, err
}
func (c *Client) ReadFile(ctx context.Context, req *ReadFileRequest) (*ReadFileResponse, error) {
	out := new(ReadFileResponse)
	err := c.conn.Invoke(ctx, "/"+serviceName+"/ReadFile", req, out, grpc.ForceCodec(JSONCodec{}))
	return out, err
}
func (c *Client) WriteFile(ctx context.Context, req *WriteFileRequest) (*WriteFileResponse, error) {
	out := new(WriteFileResponse)
	err := c.conn.Invoke(ctx, "/"+serviceName+"/WriteFile", req, out, grpc.ForceCodec(JSONCodec{}))
	return out, err
}

// MemoryExecutor is intentionally small; production replaces it with the
// process/filesystem implementation inside the isolated envd container.
type MemoryExecutor struct {
	mu    sync.RWMutex
	files map[string][]byte
}

func NewMemoryExecutor() *MemoryExecutor { return &MemoryExecutor{files: make(map[string][]byte)} }
func (e *MemoryExecutor) Exec(_ context.Context, command string) (ExecResponse, error) {
	return ExecResponse{ExitCode: 0, Stdout: "executed: " + command + "\n"}, nil
}
func (e *MemoryExecutor) ReadFile(_ context.Context, path string) ([]byte, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	data, ok := e.files[path]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return append([]byte(nil), data...), nil
}
func (e *MemoryExecutor) WriteFile(_ context.Context, path string, data []byte) (int64, error) {
	if path == "" || strings.Contains(path, "..") {
		return 0, errors.New("invalid path")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.files[path] = append([]byte(nil), data...)
	return int64(len(data)), nil
}
