package operator

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/owen-kuai/kubebox/internal/kubeapi"
)

func claimWithClassRef(ref string) *kubeapi.SandboxClaim {
	return &kubeapi.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "claim-1", Namespace: "sandbox-tenant-a"},
		Spec:       kubeapi.SandboxClaimSpec{ClassRef: ref, Template: "python", TTLSeconds: 60},
	}
}

func TestSandboxClaimValidatorAllowsKnownTiers(t *testing.T) {
	v := &SandboxClaimValidator{}
	for _, ref := range []string{"", "runc", "gvisor", "kata"} {
		if _, err := v.ValidateCreate(context.Background(), claimWithClassRef(ref)); err != nil {
			t.Fatalf("classRef %q should be allowed: %v", ref, err)
		}
	}
}

func TestSandboxClaimValidatorRejectsUnknownTier(t *testing.T) {
	v := &SandboxClaimValidator{}
	if _, err := v.ValidateCreate(context.Background(), claimWithClassRef("privileged")); err == nil {
		t.Fatal("expected rejection for unknown classRef")
	}
	if _, err := v.ValidateCreate(context.Background(), claimWithClassRef("kata-qemu")); err == nil {
		t.Fatal("expected rejection for unknown classRef")
	}
}

func TestSandboxClaimValidatorUpdateRejectsBadTier(t *testing.T) {
	v := &SandboxClaimValidator{}
	old := claimWithClassRef("gvisor")
	bad := claimWithClassRef("privileged")
	if _, err := v.ValidateUpdate(context.Background(), old, bad); err == nil {
		t.Fatal("expected update rejection")
	}
}

func TestSandboxClaimValidatorDeleteAlwaysAllowed(t *testing.T) {
	v := &SandboxClaimValidator{}
	if _, err := v.ValidateDelete(context.Background(), claimWithClassRef("privileged")); err != nil {
		t.Fatalf("delete should always be allowed: %v", err)
	}
}
