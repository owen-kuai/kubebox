package persistence

import (
	"context"
	"time"
)

// GovernanceStore is the durable boundary consumed by controllers and the API.
// Implementations must preserve the transaction semantics documented by SQLStore.
type GovernanceStore interface {
	// SetQuota installs or raises the owner's concurrent limit. Refusing to
	// lower below current usage is the implementation's responsibility.
	SetQuota(ctx context.Context, tenantID, ownerID string, limit int) error
	ReserveAllocation(ctx context.Context, tenantID, ownerID, allocationID, sandboxID string, now time.Time) error
	ReleaseAllocation(ctx context.Context, tenantID, ownerID, allocationID string, now time.Time) error
	InsertIdempotencyPending(ctx context.Context, tenantID, ownerID, key, requestHash string, expiresAt time.Time) error
}

var _ GovernanceStore = (*SQLStore)(nil)
