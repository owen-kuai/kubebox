package envd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"google.golang.org/grpc/metadata"
)

// HTTPGateway exposes the same envd execution surface (commands / files /
// health) over plain HTTP. It shares the Executor and the exact identity +
// scope semantics of the gRPC Server: an internal sandbox identity header is
// required and scopes are enforced per endpoint, so a client cannot reach the
// executor without a properly scoped, sandbox-bound credential.
//
// Deployment model:
//
//	envd-proxy (JWT check + scope map)  ->  HTTPGateway (identity re-check +
//	                                          executor call)  ->  ProcessExecutor
//
// The proxy is the public edge that validates short-lived envid credentials;
// the gateway runs in the trusted envd container and re-validates sandbox
// identity before touching the filesystem / process.
type HTTPGateway struct {
	SandboxID string
	Executor  Executor
}

func NewHTTPGateway(sandboxID string, executor Executor) *HTTPGateway {
	return &HTTPGateway{SandboxID: sandboxID, Executor: executor}
}

func (g *HTTPGateway) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", g.handleHealth)
	mux.HandleFunc("/commands", g.handleCommands)
	mux.HandleFunc("/files/read", g.handleFilesRead)
	mux.HandleFunc("/files/write", g.handleFilesWrite)
	return mux
}

func (g *HTTPGateway) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeGatewayJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleCommands maps POST /commands { "command": "..." } -> Exec.
func (g *HTTPGateway) handleCommands(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeGatewayJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST required"})
		return
	}
	if err := g.authorize(r, "commands"); err != nil {
		writeGatewayError(w, err)
		return
	}
	if g.Executor == nil {
		writeGatewayJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "executor unavailable"})
		return
	}
	var req ExecRequest
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeGatewayJSON(w, http.StatusBadRequest, map[string]string{"error": "read body"})
		return
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeGatewayJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if strings.TrimSpace(req.Command) == "" {
		writeGatewayJSON(w, http.StatusBadRequest, map[string]string{"error": "command is required"})
		return
	}
	response, err := g.Executor.Exec(r.Context(), req.Command)
	if err != nil {
		writeGatewayJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeGatewayJSON(w, http.StatusOK, response)
}

// handleFilesRead maps GET /files/read?path=... -> Executor.ReadFile.
func (g *HTTPGateway) handleFilesRead(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeGatewayJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET required"})
		return
	}
	if err := g.authorize(r, "files"); err != nil {
		writeGatewayError(w, err)
		return
	}
	if g.Executor == nil {
		writeGatewayJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "executor unavailable"})
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		writeGatewayJSON(w, http.StatusBadRequest, map[string]string{"error": "path is required"})
		return
	}
	data, err := g.Executor.ReadFile(r.Context(), path)
	if err != nil {
		writeGatewayJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeGatewayJSON(w, http.StatusOK, map[string]string{"data": string(data)})
}

// handleFilesWrite maps POST /files/write { "path": "...", "data": "..." } -> Executor.WriteFile.
func (g *HTTPGateway) handleFilesWrite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeGatewayJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST required"})
		return
	}
	if err := g.authorize(r, "files"); err != nil {
		writeGatewayError(w, err)
		return
	}
	if g.Executor == nil {
		writeGatewayJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "executor unavailable"})
		return
	}
	var req struct {
		Path string `json:"path"`
		Data string `json:"data"`
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeGatewayJSON(w, http.StatusBadRequest, map[string]string{"error": "read body"})
		return
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeGatewayJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if req.Path == "" {
		writeGatewayJSON(w, http.StatusBadRequest, map[string]string{"error": "path is required"})
		return
	}
	written, err := g.Executor.WriteFile(r.Context(), req.Path, []byte(req.Data))
	if err != nil {
		writeGatewayJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeGatewayJSON(w, http.StatusOK, map[string]int64{"bytesWritten": written})
}

// authorize mirrors Server.authorize: identity header is mandatory, must match
// this gateway's SandboxID, and the requested scope must be granted.
func (g *HTTPGateway) authorize(r *http.Request, scope string) error {
	values := r.Header.Values("X-Kubebox-Sandbox-ID")
	if len(values) == 0 || strings.TrimSpace(values[0]) == "" {
		return statusError(codesUnauthenticated, "internal sandbox identity is required")
	}
	if strings.TrimSpace(values[0]) != g.SandboxID {
		return statusError(codesPermissionDenied, "sandbox identity rejected")
	}
	for _, headerVal := range r.Header.Values("X-Kubebox-Scope") {
		for _, granted := range strings.Split(headerVal, ",") {
			if strings.TrimSpace(granted) == scope {
				return nil
			}
		}
	}
	return statusError(codesPermissionDenied, "scope rejected")
}

// withIncomingIdentity wraps a context with the gateway's own identity so the
// shared authorize helpers (and any future gRPC-bound path) behave identically.
func (g *HTTPGateway) withIncomingIdentity(ctx context.Context) context.Context {
	md := metadata.Pairs("x-kubebox-sandbox-id", g.SandboxID)
	return metadata.NewIncomingContext(ctx, md)
}

// statusError is a tiny stand-in for grpc status codes in the HTTP layer.
func statusError(code int, msg string) error {
	return httpStatusError{code: code, msg: msg}
}

type httpStatusError struct {
	code int
	msg  string
}

func (e httpStatusError) Error() string { return e.msg }

const (
	codesUnauthenticated  = http.StatusUnauthorized
	codesPermissionDenied = http.StatusForbidden
)

func writeGatewayJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeGatewayError(w http.ResponseWriter, err error) {
	var se httpStatusError
	if errors.As(err, &se) {
		writeGatewayJSON(w, se.code, map[string]string{"error": se.msg})
		return
	}
	writeGatewayJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
}
