package sandbox

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPCreateGetDelete(t *testing.T) {
	store := NewStore()
	if err := store.SetQuota("tenant-a", "owner-1", 1); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewHandler(store))
	defer server.Close()

	body, _ := json.Marshal(CreateRequest{TenantID: "tenant-a", Template: "python"})
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/sandboxes", bytes.NewReader(body))
	req.Header.Set("X-Owner-ID", "owner-1")
	req.Header.Set("Idempotency-Key", "create-1")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d", res.StatusCode)
	}
	var created Sandbox
	if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()

	res, err = http.Get(server.URL + "/api/v1/sandboxes/" + created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d", res.StatusCode)
	}
	res.Body.Close()

	req, _ = http.NewRequest(http.MethodDelete, server.URL+"/api/v1/sandboxes/"+created.ID, nil)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("delete status = %d", res.StatusCode)
	}
	res.Body.Close()

	res, err = http.Get(server.URL + "/api/v1/sandboxes/" + created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("draining sandbox should remain queryable, got %d", res.StatusCode)
	}
	res.Body.Close()
}

func TestHTTPHealth(t *testing.T) {
	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	NewHandler(NewStore()).ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("health status = %d", res.Code)
	}
}
