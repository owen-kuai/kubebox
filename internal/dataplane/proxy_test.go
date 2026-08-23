package dataplane

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestProxyValidatesScopedCredentialAndStripsIt(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("credential leaked to envd: %q", got)
		}
		if r.Header.Get("X-Kubebox-Sandbox-ID") != "sbx-1" {
			t.Fatalf("missing sandbox identity header")
		}
		if r.URL.Path != "/commands/run" {
			t.Fatalf("backend path = %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}))
	defer backend.Close()

	secret := []byte(strings.Repeat("s", 32))
	issuer, err := NewTokenIssuer(secret)
	if err != nil {
		t.Fatal(err)
	}
	token, err := issuer.Issue("sbx-1", []string{"commands"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	target, _ := url.Parse(backend.URL)
	proxy := &Proxy{Issuer: issuer, Routes: NewRouteRegistry()}
	if err := proxy.Routes.Set("sbx-1", target); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/sandboxes/sbx-1/commands/run", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res := httptest.NewRecorder()
	proxy.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("proxy status = %d, body=%s", res.Code, res.Body.String())
	}
}

func TestProxyRejectsWrongSandboxAndScope(t *testing.T) {
	issuer, err := NewTokenIssuer([]byte(strings.Repeat("s", 32)))
	if err != nil {
		t.Fatal(err)
	}
	token, err := issuer.Issue("sbx-1", []string{"files"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	proxy := &Proxy{Issuer: issuer, Routes: NewRouteRegistry()}
	target, _ := url.Parse("http://127.0.0.1:1")
	if err := proxy.Routes.Set("sbx-1", target); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/sandboxes/sbx-1/commands/run", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res := httptest.NewRecorder()
	proxy.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", res.Code)
	}
}

func TestExpiredCredentialIsRejected(t *testing.T) {
	issuer, err := NewTokenIssuer([]byte(strings.Repeat("s", 32)))
	if err != nil {
		t.Fatal(err)
	}
	issuer.now = func() time.Time { return time.Unix(100, 0) }
	token, err := issuer.Issue("sbx-1", []string{"files"}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	issuer.now = func() time.Time { return time.Unix(102, 0) }
	if _, err := issuer.Validate(token, "sbx-1", "files"); err == nil {
		t.Fatal("expected expired token rejection")
	}
}
