package dataplane

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func jsonDecode(r *http.Request, v any) error { return json.NewDecoder(r.Body).Decode(v) }

func TestRouteClientRegister(t *testing.T) {
	var gotMethod, gotPath, gotToken string
	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotToken = r.Method, r.URL.Path, r.Header.Get("X-Kubebox-Admin-Token")
		_ = jsonDecode(r, &gotBody)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	client, err := NewRouteClient(srv.URL, "admin-secret")
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Register(context.Background(), "sbx-1", "http://10.0.0.5:8080"); err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodPost || gotPath != "/internal/v1/routes" {
		t.Fatalf("method=%s path=%s", gotMethod, gotPath)
	}
	if gotToken != "admin-secret" {
		t.Fatalf("admin token = %q", gotToken)
	}
	if gotBody["sandboxId"] != "sbx-1" || gotBody["target"] != "http://10.0.0.5:8080" {
		t.Fatalf("body = %v", gotBody)
	}
}

func TestRouteClientUnregisterTolerates404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	client, err := NewRouteClient(srv.URL, "admin-secret")
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Unregister(context.Background(), "sbx-gone"); err != nil {
		t.Fatalf("unregister should tolerate 404: %v", err)
	}
}

func TestRouteClientUnregisterPropagatesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	client, _ := NewRouteClient(srv.URL, "admin-secret")
	if err := client.Unregister(context.Background(), "sbx-1"); err == nil {
		t.Fatal("expected error from 500")
	}
}

func TestNewRouteClientValidation(t *testing.T) {
	if _, err := NewRouteClient("", "secret"); err == nil {
		t.Fatal("expected error for empty base URL")
	}
	if _, err := NewRouteClient("http://x", ""); err == nil {
		t.Fatal("expected error for empty secret")
	}
}
