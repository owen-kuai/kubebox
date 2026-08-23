package sandbox

import "testing"

func TestCreateIsIdempotentAndTenantScoped(t *testing.T) {
	s := NewStore()
	if err := s.SetQuota("tenant-a", "owner-1", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.SetQuota("tenant-b", "owner-1", 1); err != nil {
		t.Fatal(err)
	}
	req := CreateRequest{TenantID: "tenant-a", Template: "python", TTLSeconds: 60}
	first, err := s.Create("owner-1", "key-1", req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Create("owner-1", "key-1", req)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("idempotency returned different IDs: %s vs %s", first.ID, second.ID)
	}
	if _, err := s.Create("owner-1", "key-1", CreateRequest{TenantID: "tenant-a", Template: "node"}); err != ErrIdempotencyConflict {
		t.Fatalf("expected conflict, got %v", err)
	}
	other, err := s.Create("owner-1", "key-1", CreateRequest{TenantID: "tenant-b", Template: "python"})
	if err != nil {
		t.Fatal(err)
	}
	if other.TenantID != "tenant-b" {
		t.Fatalf("unexpected tenant: %s", other.TenantID)
	}
}

func TestAllocationReleaseIsIdempotent(t *testing.T) {
	s := NewStore()
	if err := s.SetQuota("tenant-a", "owner-1", 2); err != nil {
		t.Fatal(err)
	}
	sb, err := s.Create("owner-1", "key-1", CreateRequest{TenantID: "tenant-a", Template: "python"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Drain(sb.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Drain(sb.ID); err != nil {
		t.Fatal(err)
	}
	q, _ := s.GetQuota("tenant-a", "owner-1")
	if q.CurrentCount != 0 {
		t.Fatalf("expected quota count 0, got %d", q.CurrentCount)
	}
	if _, err := s.CompleteDeletion(sb.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(sb.ID); err != ErrNotFound {
		t.Fatalf("expected deleted sandbox to be hidden, got %v", err)
	}
}

func TestQuotaIsEnforced(t *testing.T) {
	s := NewStore()
	if err := s.SetQuota("tenant-a", "owner-1", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create("owner-1", "key-1", CreateRequest{TenantID: "tenant-a", Template: "python"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create("owner-1", "key-2", CreateRequest{TenantID: "tenant-a", Template: "python"}); err != ErrQuotaExceeded {
		t.Fatalf("expected quota exceeded, got %v", err)
	}
}
