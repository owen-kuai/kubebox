package operator

import (
	"context"
	"testing"

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
			Name: "claim-1", Namespace: "sandbox-tenant-a",
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
}

func reconcileRequestFor(claim *kubeapi.SandboxClaim) reconcile.Request {
	return reconcile.Request{NamespacedName: types.NamespacedName{Namespace: claim.Namespace, Name: claim.Name}}
}
func clientKey(req reconcile.Request) client.ObjectKey { return req.NamespacedName }
