package operator

import (
	"errors"
	"testing"
	"time"
)

type fakePods struct {
	pods    map[string]Pod
	creates int
	deletes int
}

func newFakePods() *fakePods                   { return &fakePods{pods: make(map[string]Pod)} }
func (f *fakePods) key(ns, name string) string { return ns + "/" + name }
func (f *fakePods) Get(ns, name string) (Pod, error) {
	pod, ok := f.pods[f.key(ns, name)]
	if !ok {
		return Pod{}, ErrNotFound
	}
	return pod, nil
}
func (f *fakePods) Create(pod Pod) error {
	f.creates++
	key := f.key(pod.Namespace, pod.Name)
	if _, ok := f.pods[key]; ok {
		return ErrAlreadyExists
	}
	f.pods[key] = pod
	return nil
}
func (f *fakePods) Delete(ns, name string) error {
	f.deletes++
	key := f.key(ns, name)
	if _, ok := f.pods[key]; !ok {
		return ErrNotFound
	}
	pod := f.pods[key]
	pod.Deleted = true
	f.pods[key] = pod
	return nil
}

func TestReconcileCreatesOnceAndWaitsForHealth(t *testing.T) {
	pods := newFakePods()
	r := &Reconciler{Pods: pods, Now: func() time.Time { return time.Unix(100, 0) }}
	claim := &Claim{Namespace: "sandbox-tenant-a", Name: "claim-1", TenantID: "tenant-a", OwnerID: "owner-1", IdempotencyKey: "key-1", Template: "python"}
	if requeue, err := r.Reconcile(claim); err != nil || !requeue {
		t.Fatalf("first reconcile = requeue %v err %v", requeue, err)
	}
	if pods.creates != 1 {
		t.Fatalf("creates = %d", pods.creates)
	}
	podName := claim.PodName
	pod := pods.pods[pods.key(claim.Namespace, podName)]
	pod.Ready, pod.Healthy = true, true
	pods.pods[pods.key(claim.Namespace, podName)] = pod
	if requeue, err := r.Reconcile(claim); err != nil || requeue {
		t.Fatalf("ready reconcile = requeue %v err %v", requeue, err)
	}
	if claim.Phase != ClaimReady || claim.ProxyEndpoint == "" {
		t.Fatalf("claim not ready: %+v", claim)
	}
	if _, err := r.Reconcile(claim); err != nil {
		t.Fatal(err)
	}
	if pods.creates != 1 {
		t.Fatalf("leader handoff created duplicate pod: %d", pods.creates)
	}
}

func TestReconcileDeleteIsIdempotent(t *testing.T) {
	pods := newFakePods()
	r := &Reconciler{Pods: pods}
	claim := &Claim{Namespace: "sandbox-tenant-a", TenantID: "tenant-a", IdempotencyKey: "key-1", DesiredState: "Deleted"}
	if _, err := r.Reconcile(claim); err != nil {
		t.Fatal(err)
	}
	if claim.Phase != ClaimDeleted {
		t.Fatalf("phase = %s", claim.Phase)
	}
	if _, err := r.Reconcile(claim); err != nil {
		t.Fatal(err)
	}
	if pods.deletes != 2 {
		t.Fatalf("expected idempotent delete attempts, got %d", pods.deletes)
	}
}

func TestRuntimeClassAllowlist(t *testing.T) {
	for _, runtime := range []string{"runc", "gvisor", "kata"} {
		if err := ValidateRuntimeClass(runtime); err != nil {
			t.Fatal(err)
		}
	}
	if err := ValidateRuntimeClass("privileged"); err == nil {
		t.Fatal("expected invalid runtime class")
	}
	if !errors.Is(ErrNotFound, ErrNotFound) {
		t.Fatal("sentinel check")
	}
}

func TestDeterministicPodName(t *testing.T) {
	if DeterministicPodName("t", "k") != DeterministicPodName("t", "k") {
		t.Fatal("name is not deterministic")
	}
	if DeterministicPodName("t", "k") == DeterministicPodName("other", "k") {
		t.Fatal("tenant collision")
	}
}
