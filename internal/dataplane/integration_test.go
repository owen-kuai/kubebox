package dataplane_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/owen-kuai/kubebox/internal/dataplane"
	"github.com/owen-kuai/kubebox/internal/envd"
)

// TestProxyToGatewayEndToEnd wires the full data plane:
//
//	envd-proxy (JWT validation + credential strip + sandbox-id injection)
//	  -> envd HTTPGateway (identity re-check) -> ProcessExecutor (real exec)
//
// A real scoped credential issued by the control plane is passed through the
// proxy; the executor runs a real command in an isolated sandbox root.
func TestProxyToGatewayEndToEnd(t *testing.T) {
	// 1. Real process executor rooted at a temp dir.
	root := t.TempDir()
	exec, err := envd.NewProcessExecutor(root)
	if err != nil {
		t.Fatal(err)
	}

	// 2. HTTP gateway bound to that executor.
	gw := envd.NewHTTPGateway("sbx-e2e", exec)
	gwServer := httptest.NewServer(gw.Handler())
	defer gwServer.Close()

	// 3. Proxy in front of the gateway.
	secret := []byte("0123456789abcdef0123456789abcdef")
	issuer, err := dataplane.NewTokenIssuer(secret)
	if err != nil {
		t.Fatal(err)
	}
	routes := dataplane.NewRouteRegistry()
	target, _ := url.Parse(gwServer.URL)
	if err := routes.Set("sbx-e2e", target); err != nil {
		t.Fatal(err)
	}
	proxy := &dataplane.Proxy{Issuer: issuer, Routes: routes}
	proxyServer := httptest.NewServer(proxy.Handler())
	defer proxyServer.Close()

	// 4. Issue a scoped credential and run a real command through the proxy.
	token, err := issuer.Issue("sbx-e2e", []string{"commands"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequest(http.MethodPost,
		proxyServer.URL+"/v1/sandboxes/sbx-e2e/commands",
		bytes.NewReader([]byte(`{"command":"echo hello-proxy"}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200 through proxy, got %d body=%s", resp.StatusCode, string(b))
	}
	var out envd.ExecResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Stdout, "hello-proxy") {
		t.Fatalf("expected command output, got stdout=%q", out.Stdout)
	}

	// 5. Verify file writes through the proxy land inside the sandbox root
	//    (not the host filesystem).
	fileToken, _ := issuer.Issue("sbx-e2e", []string{"files"}, time.Minute)
	wreq, _ := http.NewRequest(http.MethodPost,
		proxyServer.URL+"/v1/sandboxes/sbx-e2e/files/write",
		bytes.NewReader([]byte(`{"path":"out.txt","data":"sandboxed"}`)))
	wreq.Header.Set("Content-Type", "application/json")
	wreq.Header.Set("Authorization", "Bearer "+fileToken)
	wresp, err := http.DefaultClient.Do(wreq)
	if err != nil {
		t.Fatal(err)
	}
	defer wresp.Body.Close()
	if wresp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(wresp.Body)
		t.Fatalf("file write expected 200, got %d body=%s", wresp.StatusCode, string(b))
	}

	created := filepath.Join(root, "out.txt")
	data, err := os.ReadFile(created)
	if err != nil {
		t.Fatalf("expected file in sandbox root: %v", err)
	}
	if string(data) != "sandboxed" {
		t.Fatalf("expected sandboxed content, got %q", string(data))
	}
}
