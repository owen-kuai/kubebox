package envd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const gatewaySandboxID = "sbx-http-1"

func newGatewayServer(t *testing.T) *httptest.Server {
	t.Helper()
	exec := NewMemoryExecutor()
	gw := NewHTTPGateway(gatewaySandboxID, exec)
	ts := httptest.NewServer(gw.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func gatewayDo(baseURL, method, path, body, id, scope string) (*http.Response, []byte) {
	var reader io.Reader
	if body != "" {
		reader = bytes.NewReader([]byte(body))
	}
	req, _ := http.NewRequest(method, baseURL+path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if id != "" {
		req.Header.Set("X-Kubebox-Sandbox-ID", id)
	}
	if scope != "" {
		req.Header.Set("X-Kubebox-Scope", scope)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, nil
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, data
}

func TestGatewayHealth(t *testing.T) {
	ts := newGatewayServer(t)
	resp, _ := gatewayDo(ts.URL, http.MethodGet, "/healthz", "", "", "")
	if resp == nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("health expected 200, got %v", resp)
	}
}

func TestGatewayCommandsAuthz(t *testing.T) {
	ts := newGatewayServer(t)
	cmdBody := `{"command":"echo hi"}`

	// Missing identity -> 401.
	resp, _ := gatewayDo(ts.URL, http.MethodPost, "/commands", cmdBody, "", "commands")
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing identity expected 401, got %v", resp)
	}
	// Wrong identity -> 403.
	resp, _ = gatewayDo(ts.URL, http.MethodPost, "/commands", cmdBody, "sbx-evil", "commands")
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("wrong identity expected 403, got %v", resp)
	}
	// Missing commands scope -> 403.
	resp, _ = gatewayDo(ts.URL, http.MethodPost, "/commands", cmdBody, gatewaySandboxID, "files")
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("missing scope expected 403, got %v", resp)
	}
	// Valid -> 200.
	resp, data := gatewayDo(ts.URL, http.MethodPost, "/commands", cmdBody, gatewaySandboxID, "commands")
	if resp == nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("valid exec expected 200, got %v", resp)
	}
	var out ExecResponse
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Stdout, "echo hi") {
		t.Fatalf("expected echo output, got stdout=%q", out.Stdout)
	}
}

func TestGatewayFiles(t *testing.T) {
	ts := newGatewayServer(t)
	writeBody := `{"path":"note.txt","data":"hello"}`

	resp, _ := gatewayDo(ts.URL, http.MethodPost, "/files/write", writeBody, gatewaySandboxID, "files")
	if resp == nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("write expected 200, got %v", resp)
	}
	resp, data := gatewayDo(ts.URL, http.MethodGet, "/files/read?path=note.txt", "", gatewaySandboxID, "files")
	if resp == nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("read expected 200, got %v", resp)
	}
	if !strings.Contains(string(data), "hello") {
		t.Fatalf("expected file content, got %s", string(data))
	}
}

func TestGatewayFilesPathTraversalRejected(t *testing.T) {
	ts := newGatewayServer(t)
	resp, _ := gatewayDo(ts.URL, http.MethodGet, "/files/read?path=../secret.txt", "", gatewaySandboxID, "files")
	if resp == nil || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("path traversal expected 404, got %v", resp)
	}
}
