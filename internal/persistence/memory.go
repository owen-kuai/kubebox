package persistence

import (
	"context"
	"sync"
	"time"
)

// quotaRow mirrors the t_owner_quota table for the in-memory governance store.
type quotaRow struct {
	concurrentLimit int
	currentCount    int
}

// allocationRow mirrors the t_quota_allocation table for the in-memory store.
type allocationRow struct {
	sandboxID string
	status    string // RESERVED | RELEASED
}

// MemoryGovernanceStore implements GovernanceStore without an external
// database. It mirrors the SQLStore transaction semantics so the control
// plane can run and be tested without PostgreSQL/MySQL, and swap to the
// SQLStore once a real driver is connected.
type MemoryGovernanceStore struct {
	mu          sync.Mutex
	quotas      map[string]*quotaRow
	allocations map[string]*allocationRow
	idempotency map[string]*idempotencyRow
}

type idempotencyRow struct {
	ownerID     string
	requestHash string
	status      string // PENDING | SUCCEEDED | FAILED
	resourceID  string
}

// NewMemoryGovernanceStore returns an empty in-memory governance store.
func NewMemoryGovernanceStore() *MemoryGovernanceStore {
	return &MemoryGovernanceStore{
		quotas:      make(map[string]*quotaRow),
		allocations: make(map[string]*allocationRow),
		idempotency: make(map[string]*idempotencyRow),
	}
}

// SetQuota configures the concurrent limit for an owner. It refuses to lower
// the limit below the current in-use count (mirrors SQL conditional update).
func (m *MemoryGovernanceStore) SetQuota(_ context.Context, tenantID, ownerID string, limit int) error {
	if tenantID == "" || ownerID == "" || limit < 0 {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := tenantID + "\x00" + ownerID
	row := m.quotas[key]
	if row == nil {
		row = &quotaRow{}
		m.quotas[key] = row
	}
	if limit < row.currentCount {
		return ErrQuotaBelowUsage
	}
	row.concurrentLimit = limit
	return nil
}

// GetQuota returns the current quota row snapshot and whether it exists.
func (m *MemoryGovernanceStore) GetQuota(tenantID, ownerID string) (limit, current int, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row := m.quotas[tenantID+"\x00"+ownerID]
	if row == nil {
		return 0, 0, false
	}
	return row.concurrentLimit, row.currentCount, true
}

// ReserveAllocation atomically increments the owner's current count (only if
// below the limit) and records a RESERVED allocation, in one locked step.
func (m *MemoryGovernanceStore) ReserveAllocation(ctx context.Context, tenantID, ownerID, allocationID, sandboxID string, now time.Time) error {
	_ = ctx
	if tenantID == "" || ownerID == "" || allocationID == "" || sandboxID == "" {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := tenantID + "\x00" + ownerID
	row := m.quotas[key]
	if row == nil {
		return ErrQuotaNotConfigured
	}
	if row.currentCount >= row.concurrentLimit {
		return ErrQuotaExceeded
	}
	row.currentCount++
	m.allocations[allocationID] = &allocationRow{sandboxID: sandboxID, status: "RESERVED"}
	return nil
}

// ReleaseAllocation is idempotent: only RESERVED -> RELEASED decrements the
// aggregate count. Releasing an already-released allocation is a no-op.
func (m *MemoryGovernanceStore) ReleaseAllocation(ctx context.Context, tenantID, ownerID, allocationID string, now time.Time) error {
	_ = ctx
	_ = now
	if tenantID == "" || ownerID == "" || allocationID == "" {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	alloc := m.allocations[allocationID]
	if alloc == nil {
		return ErrAllocationNotFound
	}
	if alloc.status == "RELEASED" {
		return nil
	}
	key := tenantID + "\x00" + ownerID
	row := m.quotas[key]
	if row == nil || row.currentCount <= 0 {
		return ErrQuotaCorrupt
	}
	alloc.status = "RELEASED"
	row.currentCount--
	return nil
}

// InsertIdempotencyPending claims a request fingerprint. A duplicate key is a
// no-op (mirrors ON CONFLICT DO NOTHING): the caller must compare request_hash
// and inspect the prior terminal/PENDING state.
func (m *MemoryGovernanceStore) InsertIdempotencyPending(ctx context.Context, tenantID, ownerID, key, requestHash string, expiresAt time.Time) error {
	_ = ctx
	_ = expiresAt
	if tenantID == "" || ownerID == "" || key == "" || requestHash == "" {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	fk := tenantID + "\x00" + key
	if _, exists := m.idempotency[fk]; exists {
		return nil
	}
	m.idempotency[fk] = &idempotencyRow{ownerID: ownerID, requestHash: requestHash, status: "PENDING"}
	return nil
}
