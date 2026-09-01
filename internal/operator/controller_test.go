package operator

import (
	"context"
	"errors"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/owen-kuai/kubebox/internal/kubeapi"
)

func TestSandboxClaimControllerCreatesPodAndUpdatesStatus(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := kubeapi.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	claim := &kubeapi.SandboxClaim{
		TypeMeta: metav1.TypeMeta{APIVersion: kubeapi.GroupVersion.String(), Kind: "SandboxClaim"},
		ObjectMeta: metav1.ObjectMeta{
			Name: "claim-1", Namespace: "sandbox-tenant-a", UID: types.UID("claim-uid-1"),
			Labels: map[string]string{"kubebox.io/tenant": "tenant-a", "kubebox.io/owner": "owner-1"},
		},
		Spec: kubeapi.SandboxClaimSpec{ClassRef: "gvisor", Template: "python", TTLSeconds: 60, IdempotencyKey: "key-1"},
	}
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(claim).WithObjects(claim).Build()
	reconciler := &SandboxClaimReconciler{Client: kubeClient}
	req := reconcileRequestFor(claim)
	if _, err := reconciler.Reconcile(context.Background(), req); err != nil {
		t.Fatal(err)
	}

	var gotClaim kubeapi.SandboxClaim
	if err := kubeClient.Get(context.Background(), clientKey(req), &gotClaim); err != nil {
		t.Fatal(err)
	}
	if gotClaim.Status.Phase != string(ClaimProvisioning) {
		t.Fatalf("phase = %s", gotClaim.Status.Phase)
	}
	if !containsString(gotClaim.Finalizers, claimFinalizer) {
		t.Fatalf("missing finalizer: %v", gotClaim.Finalizers)
	}

	var pods corev1.PodList
	if err := kubeClient.List(context.Background(), &pods, client.InNamespace(claim.Namespace)); err != nil {
		t.Fatal(err)
	}
	if len(pods.Items) != 1 {
		t.Fatalf("pods = %d", len(pods.Items))
	}
	pod := pods.Items[0]
	if pod.Labels["kubebox.io/workload"] != "sandbox" {
		t.Fatalf("workload label = %q", pod.Labels["kubebox.io/workload"])
	}
	if len(pod.OwnerReferences) != 1 || pod.OwnerReferences[0].UID != types.UID("claim-uid-1") {
		t.Fatalf("owner references = %+v", pod.OwnerReferences)
	}
	pod.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodInitialized, Status: corev1.ConditionTrue}, {Type: corev1.PodReady, Status: corev1.ConditionTrue}}
	pod.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: "envd", Ready: true}}
	if err := kubeClient.Status().Update(context.Background(), &pod); err != nil {
		t.Fatal(err)
	}

	if _, err := reconciler.Reconcile(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if err := kubeClient.Get(context.Background(), clientKey(req), &gotClaim); err != nil {
		t.Fatal(err)
	}
	if gotClaim.Status.Phase != string(ClaimReady) {
		t.Fatalf("phase = %s", gotClaim.Status.Phase)
	}
	if gotClaim.Status.ProxyEndpoint == "" {
		t.Fatal("proxy endpoint not set")
	}
	if gotClaim.Status.ProxyEndpoint != "envd-proxy.sandbox-system.svc:8080" {
		t.Fatalf("proxy endpoint = %q", gotClaim.Status.ProxyEndpoint)
	}
	firstExpiry := gotClaim.Status.ExpiresAt.DeepCopy()
	if _, err := reconciler.Reconcile(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if err := kubeClient.Get(context.Background(), clientKey(req), &gotClaim); err != nil {
		t.Fatal(err)
	}
	if gotClaim.Status.ExpiresAt == nil || !gotClaim.Status.ExpiresAt.Equal(firstExpiry) {
		t.Fatalf("expiresAt changed: first=%v current=%v", firstExpiry, gotClaim.Status.ExpiresAt)
	}
}

func reconcileRequestFor(claim *kubeapi.SandboxClaim) reconcile.Request {
	return reconcile.Request{NamespacedName: types.NamespacedName{Namespace: claim.Namespace, Name: claim.Name}}
}
func clientKey(req reconcile.Request) client.ObjectKey { return req.NamespacedName }

// fakeRegistrar records route registrations/unregistrations for assertions.
type fakeRegistrar struct {
	registered   []string
	unregistered []string
	registerErr  error
}

func (f *fakeRegistrar) Register(_ context.Context, sandboxID, target string) error {
	f.registered = append(f.registered, sandboxID+"="+target)
	return f.registerErr
}

func TestSandboxClaimControllerRetriesRouteRegistration(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = kubeapi.AddToScheme(scheme)
	claim := &kubeapi.SandboxClaim{
		TypeMeta:   metav1.TypeMeta{APIVersion: kubeapi.GroupVersion.String(), Kind: "SandboxClaim"},
		ObjectMeta: metav1.ObjectMeta{Name: "claim-retry", Namespace: "sandbox-tenant-a", Labels: map[string]string{"kubebox.io/tenant": "tenant-a", "kubebox.io/owner": "owner-1"}},
		Spec:       kubeapi.SandboxClaimSpec{ClassRef: "gvisor", Template: "python", TTLSeconds: 60, IdempotencyKey: "key-retry"},
	}
	podName := DeterministicPodName("tenant-a", "key-retry")
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: claim.Namespace},
		Status:     corev1.PodStatus{PodIP: "10.0.0.9", Conditions: []corev1.PodCondition{{Type: corev1.PodInitialized, Status: corev1.ConditionTrue}, {Type: corev1.PodReady, Status: corev1.ConditionTrue}}, ContainerStatuses: []corev1.ContainerStatus{{Name: "envd", Ready: true}}},
	}
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(claim, pod).WithObjects(claim, pod).Build()
	registrar := &fakeRegistrar{registerErr: errors.New("proxy unavailable")}
	reconciler := &SandboxClaimReconciler{Client: kubeClient, Registrar: registrar, Now: func() time.Time { return time.Unix(100, 0) }}

	result, err := reconciler.Reconcile(context.Background(), reconcileRequestFor(claim))
	if err != nil {
		t.Fatal(err)
	}
	if result.RequeueAfter != 5*time.Second {
		t.Fatalf("requeue = %s", result.RequeueAfter)
	}
	registrar.registerErr = nil
	result, err = reconciler.Reconcile(context.Background(), reconcileRequestFor(claim))
	if err != nil {
		t.Fatal(err)
	}
	if len(registrar.registered) != 2 || result.RequeueAfter <= 0 {
		t.Fatalf("registrations=%v requeue=%s", registrar.registered, result.RequeueAfter)
	}
}
func (f *fakeRegistrar) Unregister(_ context.Context, sandboxID string) error {
	f.unregistered = append(f.unregistered, sandboxID)
	return nil
}

func TestSandboxClaimControllerRegistersRouteOnReady(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := kubeapi.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	claim := &kubeapi.SandboxClaim{
		TypeMeta: metav1.TypeMeta{APIVersion: kubeapi.GroupVersion.String(), Kind: "SandboxClaim"},
		ObjectMeta: metav1.ObjectMeta{
			Name: "claim-route", Namespace: "sandbox-tenant-a",
			Labels: map[string]string{"kubebox.io/tenant": "tenant-a", "kubebox.io/owner": "owner-1"},
		},
		Spec: kubeapi.SandboxClaimSpec{ClassRef: "gvisor", Template: "python", TTLSeconds: 60, IdempotencyKey: "key-route"},
	}
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(claim).WithObjects(claim).Build()
	registrar := &fakeRegistrar{}
	reconciler := &SandboxClaimReconciler{Client: kubeClient, Registrar: registrar}
	req := reconcileRequestFor(claim)

	// First reconcile provisions the Pod (Provisioning, no route yet).
	if _, err := reconciler.Reconcile(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if len(registrar.registered) != 0 {
		t.Fatalf("route registered before Ready: %v", registrar.registered)
	}

	// Mark the Pod Ready with an IP, then reconcile again -> route registered.
	var pods corev1.PodList
	if err := kubeClient.List(context.Background(), &pods, client.InNamespace(claim.Namespace)); err != nil {
		t.Fatal(err)
	}
	pod := pods.Items[0]
	pod.Status.PodIP = "10.0.0.42"
	pod.Status.Conditions = []corev1.PodCondition{
		{Type: corev1.PodInitialized, Status: corev1.ConditionTrue},
		{Type: corev1.PodReady, Status: corev1.ConditionTrue},
	}
	pod.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: "envd", Ready: true}}
	if err := kubeClient.Status().Update(context.Background(), &pod); err != nil {
		t.Fatal(err)
	}

	if _, err := reconciler.Reconcile(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if len(registrar.registered) != 1 {
		t.Fatalf("registered = %v", registrar.registered)
	}
	if want := "claim-route=http://10.0.0.42:8080"; registrar.registered[0] != want {
		t.Fatalf("registered = %q, want %q", registrar.registered[0], want)
	}
}

func TestSandboxClaimControllerUnregistersRouteOnDelete(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = kubeapi.AddToScheme(scheme)
	claim := &kubeapi.SandboxClaim{
		TypeMeta: metav1.TypeMeta{APIVersion: kubeapi.GroupVersion.String(), Kind: "SandboxClaim"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "claim-del",
			Namespace: "sandbox-tenant-a",
			Labels:    map[string]string{"kubebox.io/tenant": "tenant-a", "kubebox.io/owner": "owner-1"},
		},
		Spec:   kubeapi.SandboxClaimSpec{ClassRef: "gvisor", Template: "python", TTLSeconds: 60, IdempotencyKey: "key-del", DesiredState: "Deleted"},
		Status: kubeapi.SandboxClaimStatus{SandboxRef: "claim-del"},
	}
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(claim).WithObjects(claim).Build()
	registrar := &fakeRegistrar{}
	reconciler := &SandboxClaimReconciler{Client: kubeClient, Registrar: registrar}

	if _, err := reconciler.Reconcile(context.Background(), reconcileRequestFor(claim)); err != nil {
		t.Fatal(err)
	}
	if len(registrar.unregistered) != 1 || registrar.unregistered[0] != "claim-del" {
		t.Fatalf("unregistered = %v", registrar.unregistered)
	}
}
