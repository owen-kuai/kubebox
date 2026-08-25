package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/owen-kuai/kubebox/internal/persistence"
)

type Phase string

const (
	PhaseProvisioning Phase = "Provisioning"
	PhaseReady        Phase = "Ready"
	PhaseDraining     Phase = "Draining"
	PhaseDeleted      Phase = "Deleted"
	PhaseFailed       Phase = "Failed"
)

type AllocationStatus string

const (
	AllocationReserved AllocationStatus = "RESERVED"
	AllocationReleased AllocationStatus = "RELEASED"
)

type IdempotencyStatus string

const (
	IdempotencyPending   IdempotencyStatus = "PENDING"
	IdempotencySucceeded IdempotencyStatus = "SUCCEEDED"
	IdempotencyFailed    IdempotencyStatus = "FAILED"
)

type Quota struct {
	TenantID        string `json:"tenantId"`
	OwnerID         string `json:"ownerId"`
	ConcurrentLimit int    `json:"concurrentLimit"`
	CurrentCount    int    `json:"currentCount"`
}

type CreateRequest struct {
	TenantID     string            `json:"tenantId"`
	Template     string            `json:"template"`
	RuntimeClass string            `json:"runtimeClass,omitempty"`
	TTLSeconds   int               `json:"ttlSeconds,omitempty"`
	Resources    map[string]string `json:"resources,omitempty"`
}

type Sandbox struct {
	ID             string            `json:"sandboxId"`
	TenantID       string            `json:"tenantId"`
	OwnerID        string            `json:"ownerId"`
	Template       string            `json:"template"`
	RuntimeClass   string            `json:"runtimeClass"`
	Resources      map[string]string `json:"resources,omitempty"`
	Phase          Phase             `json:"phase"`
	AllocationID   string            `json:"allocationId"`
	IdempotencyKey string            `json:"-"`
	ExpiresAt      time.Time         `json:"expiresAt"`
	CreatedAt      time.Time         `json:"createdAt"`
	UpdatedAt      time.Time         `json:"updatedAt"`
}

type IdempotencyRecord struct {
	TenantID     string
	OwnerID      string
	Key          string
	RequestHash  string
	Status       IdempotencyStatus
	ResourceID   string
	LeaseVersion uint64
	ExpiresAt    time.Time
}

type Allocation struct {
	ID        string
	TenantID  string
	OwnerID   string
	SandboxID string
	Status    AllocationStatus
}

type Store struct {
	mu          sync.Mutex
	sequence    uint64
	sandboxes   map[string]*Sandbox
	allocations map[string]*Allocation
	idempotency map[string]*IdempotencyRecord
	quotas      map[string]*Quota

	// gov is the durable governance boundary. When set, quota/allocation and
	// idempotency writes are delegated to it; the in-memory maps stay in sync
	// as a projection for reads. When nil, the store behaves as a pure
	// in-memory MVP (default).
	gov persistence.GovernanceStore
}

func NewStore() *Store {
	return &Store{
		sandboxes:   make(map[string]*Sandbox),
		allocations: make(map[string]*Allocation),
		idempotency: make(map[string]*IdempotencyRecord),
		quotas:      make(map[string]*Quota),
	}
}

// NewStoreWithGovernance returns a Store that delegates quota/allocation and
// idempotency writes to the given durability boundary (e.g. SQLStore). Reads
// still use the in-memory projection for low latency.
func NewStoreWithGovernance(gov persistence.GovernanceStore) (*Store, error) {
	if gov == nil {
		return nil, errors.New("governance store is required")
	}
	return &Store{
		sandboxes:   make(map[string]*Sandbox),
		allocations: make(map[string]*Allocation),
		idempotency: make(map[string]*IdempotencyRecord),
		quotas:      make(map[string]*Quota),
		gov:         gov,
	}, nil
}

func (s *Store) SetQuota(tenantID, ownerID string, limit int) error {
	if tenantID == "" || ownerID == "" || limit < 0 {
		return ErrInvalidRequest
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := quotaKey(tenantID, ownerID)
	q := s.quotas[key]
	if q == nil {
		q = &Quota{TenantID: tenantID, OwnerID: ownerID}
		s.quotas[key] = q
	}
	if limit < q.CurrentCount {
		return ErrQuotaBelowUsage
	}
	// Persist to the durable boundary first; on failure abort without touching
	// the in-memory projection.
	if s.gov != nil {
		if err := s.gov.SetQuota(context.Background(), tenantID, ownerID, limit); err != nil {
			return mapGovError(err)
		}
	}
	q.ConcurrentLimit = limit
	return nil
}

func (s *Store) GetQuota(tenantID, ownerID string) (Quota, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q, ok := s.quotas[quotaKey(tenantID, ownerID)]
	if !ok {
		return Quota{}, false
	}
	return *q, true
}

func (s *Store) Create(actorOwnerID, idempotencyKey string, req CreateRequest) (Sandbox, error) {
	if actorOwnerID == "" || idempotencyKey == "" || req.TenantID == "" || req.Template == "" {
		return Sandbox{}, ErrInvalidRequest
	}
	if req.TTLSeconds <= 0 {
		req.TTLSeconds = 1800
	}
	if req.RuntimeClass == "" {
		req.RuntimeClass = "gvisor"
	}
	hash := requestHash(req)
	idemKey := req.TenantID + "\x00" + idempotencyKey

	s.mu.Lock()
	defer s.mu.Unlock()
	if prior := s.idempotency[idemKey]; prior != nil {
		if prior.RequestHash != hash || prior.OwnerID != actorOwnerID {
			return Sandbox{}, ErrIdempotencyConflict
		}
		switch prior.Status {
		case IdempotencyPending:
			return Sandbox{}, ErrIdempotencyPending
		case IdempotencySucceeded:
			if sb := s.sandboxes[prior.ResourceID]; sb != nil {
				return cloneSandbox(sb), nil
			}
			return Sandbox{}, ErrResourceGone
		default:
			return Sandbox{}, ErrIdempotencyFailed
		}
	}

	now := time.Now().UTC()
	s.sequence++
	sandboxID := fmt.Sprintf("sbx-%08d", s.sequence)
	allocationID := fmt.Sprintf("alloc-%08d", s.sequence)
	s.idempotency[idemKey] = &IdempotencyRecord{
		TenantID: req.TenantID, OwnerID: actorOwnerID, Key: idempotencyKey,
		RequestHash: hash, Status: IdempotencyPending, LeaseVersion: 1,
		ExpiresAt: now.Add(time.Duration(req.TTLSeconds+7200) * time.Second),
	}

	if s.gov != nil {
		// Durable boundary is authoritative for the conditional quota slot and
		// allocation CAS. On success, mirror the projection so in-memory reads
		// and releases stay consistent with the durable count.
		if err := s.gov.ReserveAllocation(context.Background(), req.TenantID, actorOwnerID, allocationID, sandboxID, now); err != nil {
			delete(s.idempotency, idemKey)
			return Sandbox{}, mapGovError(err)
		}
		key := quotaKey(req.TenantID, actorOwnerID)
		proj := s.quotas[key]
		if proj == nil {
			proj = &Quota{TenantID: req.TenantID, OwnerID: actorOwnerID}
			s.quotas[key] = proj
		}
		proj.CurrentCount++
	} else {
		quota := s.quotas[quotaKey(req.TenantID, actorOwnerID)]
		if quota == nil {
			return Sandbox{}, ErrQuotaNotConfigured
		}
		if quota.CurrentCount >= quota.ConcurrentLimit {
			return Sandbox{}, ErrQuotaExceeded
		}
		// This in-memory transaction models the DB conditional update and allocation CAS.
		quota.CurrentCount++
	}
	s.allocations[allocationID] = &Allocation{ID: allocationID, TenantID: req.TenantID, OwnerID: actorOwnerID, SandboxID: sandboxID, Status: AllocationReserved}
	sb := &Sandbox{ID: sandboxID, TenantID: req.TenantID, OwnerID: actorOwnerID, Template: req.Template, RuntimeClass: req.RuntimeClass, Resources: cloneMap(req.Resources), Phase: PhaseReady, AllocationID: allocationID, IdempotencyKey: idempotencyKey, ExpiresAt: now.Add(time.Duration(req.TTLSeconds) * time.Second), CreatedAt: now, UpdatedAt: now}
	s.sandboxes[sandboxID] = sb
	prior := s.idempotency[idemKey]
	prior.Status = IdempotencySucceeded
	prior.ResourceID = sandboxID
	return cloneSandbox(sb), nil
}

func (s *Store) Get(id string) (Sandbox, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sb := s.sandboxes[id]
	if sb == nil || sb.Phase == PhaseDeleted {
		return Sandbox{}, ErrNotFound
	}
	return cloneSandbox(sb), nil
}

func (s *Store) Drain(id string) (Sandbox, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sb := s.sandboxes[id]
	if sb == nil {
		return Sandbox{}, ErrNotFound
	}
	if sb.Phase == PhaseDraining {
		return cloneSandbox(sb), nil
	}
	if sb.Phase != PhaseReady && sb.Phase != PhaseProvisioning {
		return Sandbox{}, ErrInvalidTransition
	}
	if err := s.releaseAllocationLocked(sb.AllocationID); err != nil {
		return Sandbox{}, err
	}
	sb.Phase = PhaseDraining
	sb.UpdatedAt = time.Now().UTC()
	return cloneSandbox(sb), nil
}

func (s *Store) CompleteDeletion(id string) (Sandbox, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sb := s.sandboxes[id]
	if sb == nil {
		return Sandbox{}, ErrNotFound
	}
	if sb.Phase != PhaseDraining && sb.Phase != PhaseFailed {
		return Sandbox{}, ErrInvalidTransition
	}
	sb.Phase = PhaseDeleted
	sb.UpdatedAt = time.Now().UTC()
	return cloneSandbox(sb), nil
}

func (s *Store) releaseAllocationLocked(allocationID string) error {
	allocation := s.allocations[allocationID]
	if allocation == nil {
		return ErrAllocationNotFound
	}
	if allocation.Status == AllocationReleased {
		return nil
	}
	if s.gov != nil {
		// Durable boundary owns the RESERVED -> RELEASED CAS and aggregate
		// decrement. Idempotent at the store level: already-released is a
		// no-op. Mirror the projection only on success.
		if err := s.gov.ReleaseAllocation(context.Background(), allocation.TenantID, allocation.OwnerID, allocationID, time.Now().UTC()); err != nil {
			return mapGovError(err)
		}
	}
	allocation.Status = AllocationReleased
	quota := s.quotas[quotaKey(allocation.TenantID, allocation.OwnerID)]
	if quota == nil || quota.CurrentCount <= 0 {
		return ErrQuotaCorrupt
	}
	quota.CurrentCount--
	return nil
}

func requestHash(req CreateRequest) string {
	b, _ := json.Marshal(req)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// mapGovError translates a governance-boundary error into a domain error so
// the control plane exposes a stable vocabulary to callers.
func mapGovError(err error) error {
	switch {
	case errors.Is(err, persistence.ErrQuotaExceeded):
		return ErrQuotaExceeded
	case errors.Is(err, persistence.ErrQuotaNotConfigured):
		return ErrQuotaNotConfigured
	case errors.Is(err, persistence.ErrQuotaBelowUsage):
		return ErrQuotaBelowUsage
	case errors.Is(err, persistence.ErrQuotaCorrupt):
		return ErrQuotaCorrupt
	case errors.Is(err, persistence.ErrAllocationNotFound):
		return ErrAllocationNotFound
	case errors.Is(err, persistence.ErrAllocationState):
		return ErrAllocationNotFound
	default:
		return err
	}
}

func quotaKey(tenantID, ownerID string) string { return tenantID + "\x00" + ownerID }

func cloneSandbox(in *Sandbox) Sandbox {
	out := *in
	out.Resources = cloneMap(in.Resources)
	return out
}

func cloneMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

var (
	ErrInvalidRequest      = errors.New("invalid request")
	ErrQuotaNotConfigured  = errors.New("quota not configured")
	ErrQuotaExceeded       = errors.New("quota exceeded")
	ErrQuotaBelowUsage     = errors.New("quota below current usage")
	ErrQuotaCorrupt        = errors.New("quota ledger is inconsistent")
	ErrAllocationNotFound  = errors.New("allocation not found")
	ErrNotFound            = errors.New("sandbox not found")
	ErrResourceGone        = errors.New("idempotent resource is gone")
	ErrInvalidTransition   = errors.New("invalid sandbox transition")
	ErrIdempotencyConflict = errors.New("idempotency key reused with different request")
	ErrIdempotencyPending  = errors.New("idempotency request is still processing")
	ErrIdempotencyFailed   = errors.New("idempotency request previously failed")
)
