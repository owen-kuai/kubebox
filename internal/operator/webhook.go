package operator

import (
	"context"
	"fmt"

	"github.com/owen-kuai/kubebox/internal/kubeapi"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// SandboxClaimValidator rejects SandboxClaims whose classRef falls outside the
// allowed isolation tiers {runc, gvisor, kata}. An empty classRef is allowed
// and defaults to gvisor. This closes the isolation-downgrade hole (a caller
// requesting an unknown or privileged runtime that would otherwise be silently
// mapped to gvisor, or later interpreted as a stronger/weaker tier than the
// SandboxClass policy intends).
type SandboxClaimValidator struct{}

var _ admission.Validator[*kubeapi.SandboxClaim] = (*SandboxClaimValidator)(nil)

func (v *SandboxClaimValidator) ValidateCreate(_ context.Context, obj *kubeapi.SandboxClaim) (admission.Warnings, error) {
	return nil, validateClassRef(obj.Spec.ClassRef)
}

func (v *SandboxClaimValidator) ValidateUpdate(_ context.Context, _, newObj *kubeapi.SandboxClaim) (admission.Warnings, error) {
	return nil, validateClassRef(newObj.Spec.ClassRef)
}

func (v *SandboxClaimValidator) ValidateDelete(_ context.Context, _ *kubeapi.SandboxClaim) (admission.Warnings, error) {
	return nil, nil
}

// validateClassRef enforces the isolation-tier allowlist. An empty classRef is
// the default tier (gvisor) and is permitted.
func validateClassRef(classRef string) error {
	if classRef == "" {
		return nil
	}
	if err := ValidateRuntimeClass(classRef); err != nil {
		return fmt.Errorf("spec.classRef invalid: %w", err)
	}
	return nil
}

// SetupWebhook registers the validating admission webhook for SandboxClaim.
func SetupWebhook(mgr manager.Manager) error {
	return builder.WebhookManagedBy(mgr, &kubeapi.SandboxClaim{}).
		WithValidator(&SandboxClaimValidator{}).
		Complete()
}
