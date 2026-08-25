package sandbox

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/owen-kuai/kubebox/internal/persistence"
)

// TestHTTPGovernanceBoundary verifies that when the HTTP control plane is
// backed by a GovernanceStore (the durability boundary), quota enforcement and
// release idempotency still hold end-to-end through the real handler.
func TestHTTPGovernanceBoundary(t *testing.T) {
	gov := persistence.NewMemoryGovernanceStore()
	store, err := NewStoreWithGovernance(gov)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetQuota("tenant-a", "owner-1", 1); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewHandler(store))
	defer server.Close()

	create := func(key string) (*http.Response, *Sandbox) {
		body, _ := json.Marshal(CreateRequest{TenantID: "tenant-a", Template: "python"})
		req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/sandboxes", bytes.NewReader(body))
		req.Header.Set("X-Owner-ID", "owner-1")
		req.Header.Set("Idempotency-Key", key)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		sb := &Sandbox{}
		if res.Body != nil {
			_ = json.NewDecoder(res.Body).Decode(sb)
			res.Body.Close()
		}
		return res, sb
	}

	// First create succeeds.
	res, created := create("create-1")
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("first create status = %d", res.StatusCode)
	}

	// Second concurrent slot is refused by the governance reserve.
	res, _ = create("create-2")
	if res.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("quota should be enforced via governance, got %d", res.StatusCode)
	}

	// Deleting releases the slot; governance count returns to zero.
	req, _ := http.NewRequest(http.MethodDelete, server.URL+"/api/v1/sandboxes/"+created.ID, nil)
	del, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if del.StatusCode != http.StatusAccepted {
		t.Fatalf("delete status = %d", del.StatusCode)
	}
	del.Body.Close()

	// A new sandbox may now be created: the release path decremented.
	res, _ = create("create-3")
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create after release status = %d", res.StatusCode)
	}
}

// TestGovernanceReleaseIdempotent ensures a released sandbox's extra Drain
// does not corrupt the aggregate count (release is idempotent).
func TestGovernanceReleaseIdempotent(t *testing.T) {
	gov := persistence.NewMemoryGovernanceStore()
	store, err := NewStoreWithGovernance(gov)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetQuota("tenant-a", "owner-1", 2); err != nil {
		t.Fatal(err)
	}
	s1, err := store.Create("owner-1", "k1", CreateRequest{TenantID: "tenant-a", Template: "python"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create("owner-1", "k2", CreateRequest{TenantID: "tenant-a", Template: "python"}); err != nil {
		t.Fatal(err)
	}

	if _, err := store.Drain(s1.ID); err != nil {
		t.Fatal(err)
	}
	// Draining again is a no-op and must not over-decrement.
	if _, err := store.Drain(s1.ID); err != nil {
		t.Fatal(err)
	}

	limit, current, ok := gov.GetQuota("tenant-a", "owner-1")
	if !ok {
		t.Fatal("quota row missing")
	}
	if current != 1 {
		t.Fatalf("expected aggregate count 1 after one idempotent release, got %d (limit %d)", current, limit)
	}
}
