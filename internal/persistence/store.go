package persistence

import (
	"context"
	"time"
)

// GovernanceStore is the durable boundary consumed by controllers and the API.
// Implementations must preserve the transaction semantics documented by SQLStore.
type GovernanceStore interface {
	ReserveAllocation(ctx context.Context, tenantID, ownerID, allocationID, sandboxID string, now time.Time) error
	ReleaseAllocation(ctx context.Context, tenantID, ownerID, allocationID string, now time.Time) error
	InsertIdempotencyPending(ctx context.Context, tenantID, ownerID, key, requestHash string, expiresAt time.Time) error
}

var _ GovernanceStore = (*SQLStore)(nil)
