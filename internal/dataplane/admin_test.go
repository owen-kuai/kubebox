package dataplane

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

const testAdminSecret = "top-secret-admin-token"

func mustParseURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}

func adminRequest(method, path, body string, authorized bool) *http.Request {
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	if authorized {
		req.Header.Set("X-Kubebox-Admin-Token", testAdminSecret)
	}
	return req
}

func TestAdminRegisterRequiresAuth(t *testing.T) {
	admin := NewAdmin(NewRouteRegistry(), testAdminSecret)
	res := httptest.NewRecorder()
	admin.ServeHTTP(res, adminRequest(http.MethodPost, "/internal/v1/routes",
		`{"sandboxId":"sbx-1","target":"http://127.0.0.1:8080"}`, false))
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized register = %d", res.Code)
	}
}

func TestAdminRegisterAndList(t *testing.T) {
	admin := NewAdmin(NewRouteRegistry(), testAdminSecret)

	res := httptest.NewRecorder()
	admin.ServeHTTP(res, adminRequest(http.MethodPost, "/internal/v1/routes",
		`{"sandboxId":"sbx-1","target":"http://127.0.0.1:8080"}`, true))
	if res.Code != http.StatusCreated {
		t.Fatalf("register = %d body=%s", res.Code, res.Body.String())
	}

	// The route is now visible to the proxy registry.
	if _, ok := admin.Routes.Get("sbx-1"); !ok {
		t.Fatal("route not registered")
	}

	list := httptest.NewRecorder()
	admin.ServeHTTP(list, adminRequest(http.MethodGet, "/internal/v1/routes", "", true))
	if list.Code != http.StatusOK {
		t.Fatalf("list = %d", list.Code)
	}
	var routes map[string]interface{}
	if err := json.Unmarshal(list.Body.Bytes(), &routes); err != nil {
		t.Fatal(err)
	}
	if _, ok := routes["sbx-1"]; !ok {
		t.Fatalf("list missing sbx-1: %v", routes)
	}
}

func TestAdminRegisterRejectsInvalidTarget(t *testing.T) {
	admin := NewAdmin(NewRouteRegistry(), testAdminSecret)
	res := httptest.NewRecorder()
	admin.ServeHTTP(res, adminRequest(http.MethodPost, "/internal/v1/routes",
		`{"sandboxId":"sbx-1","target":"not-a-url"}`, true))
	if res.Code != http.StatusBadRequest {
		t.Fatalf("invalid target = %d", res.Code)
	}
}

func TestAdminUnregister(t *testing.T) {
	admin := NewAdmin(NewRouteRegistry(), testAdminSecret)
	_ = admin.Routes.Set("sbx-1", mustParseURL("http://127.0.0.1:8080"))

	res := httptest.NewRecorder()
	admin.ServeHTTP(res, adminRequest(http.MethodDelete, "/internal/v1/routes/sbx-1", "", true))
	if res.Code != http.StatusOK {
		t.Fatalf("unregister = %d", res.Code)
	}
	if _, ok := admin.Routes.Get("sbx-1"); ok {
		t.Fatal("route still present after unregister")
	}

	// Second unregister is 404.
	again := httptest.NewRecorder()
	admin.ServeHTTP(again, adminRequest(http.MethodDelete, "/internal/v1/routes/sbx-1", "", true))
	if again.Code != http.StatusNotFound {
		t.Fatalf("second unregister = %d", again.Code)
	}
}

func TestAdminHealth(t *testing.T) {
	admin := NewAdmin(NewRouteRegistry(), testAdminSecret)
	res := httptest.NewRecorder()
	admin.ServeHTTP(res, adminRequest(http.MethodGet, "/healthz", "", false))
	if res.Code != http.StatusOK {
		t.Fatalf("health = %d", res.Code)
	}
}
