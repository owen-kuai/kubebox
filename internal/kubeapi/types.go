package kubeapi

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var GroupVersion = schema.GroupVersion{Group: "sandbox.kubebox.io", Version: "v1alpha1"}

// SandboxClaim is the typed Go representation of the CRD in deploy/kubernetes/mvp.yaml.
type SandboxClaim struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              SandboxClaimSpec   `json:"spec,omitempty"`
	Status            SandboxClaimStatus `json:"status,omitempty"`
}

type SandboxClaimSpec struct {
	ClassRef       string `json:"classRef"`
	Template       string `json:"template"`
	TTLSeconds     int32  `json:"ttlSeconds"`
	DesiredState   string `json:"desiredState,omitempty"`
	IdempotencyKey string `json:"idempotencyKey"`
}

type SandboxClaimStatus struct {
	Phase         string       `json:"phase,omitempty"`
	SandboxRef    string       `json:"sandboxRef,omitempty"`
	ProxyEndpoint string       `json:"proxyEndpoint,omitempty"`
	AllocationID  string       `json:"allocationID,omitempty"`
	ExpiresAt     *metav1.Time `json:"expiresAt,omitempty"`
}

type SandboxClaimList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SandboxClaim `json:"items"`
}

func (in *SandboxClaim) DeepCopyInto(out *SandboxClaim) {
	*out = *in
	out.ObjectMeta = *in.ObjectMeta.DeepCopy()
	if in.Status.ExpiresAt != nil {
		t := *in.Status.ExpiresAt
		out.Status.ExpiresAt = &t
	}
}
func (in *SandboxClaim) DeepCopy() *SandboxClaim {
	if in == nil {
		return nil
	}
	out := new(SandboxClaim)
	in.DeepCopyInto(out)
	return out
}
func (in *SandboxClaim) DeepCopyObject() runtime.Object { return in.DeepCopy() }

func (in *SandboxClaimList) DeepCopyInto(out *SandboxClaimList) {
	*out = *in
	out.Items = make([]SandboxClaim, len(in.Items))
	copy(out.Items, in.Items)
}
func (in *SandboxClaimList) DeepCopy() *SandboxClaimList {
	if in == nil {
		return nil
	}
	out := new(SandboxClaimList)
	in.DeepCopyInto(out)
	return out
}
func (in *SandboxClaimList) DeepCopyObject() runtime.Object { return in.DeepCopy() }

func AddToScheme(s *runtime.Scheme) error {
	s.AddKnownTypes(GroupVersion, &SandboxClaim{}, &SandboxClaimList{})
	metav1.AddToGroupVersion(s, GroupVersion)
	return nil
}
