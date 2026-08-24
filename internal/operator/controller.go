package operator

import (
	"context"
	"errors"
	"time"

	"github.com/owen-kuai/kubebox/internal/kubeapi"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	claimFinalizer = "sandbox.kubebox.io/finalizer"
	ownerLabel     = "kubebox.io/owner"
)

type SandboxClaimReconciler struct {
	client.Client
	PodClient PodClient
	Now       func() time.Time
}

func (r *SandboxClaimReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	var object kubeapi.SandboxClaim
	if err := r.Get(ctx, req.NamespacedName, &object); err != nil {
		if client.IgnoreNotFound(err) == nil {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}
	if r.PodClient == nil {
		r.PodClient = &KubePodClient{Client: r.Client}
	}
	now := r.Now
	if now == nil {
		now = time.Now
	}
	owner := object.Labels[ownerLabel]
	if owner == "" {
		owner = object.Labels["kubebox.io/owner-id"]
	}
	claim := &Claim{
		Namespace: object.Namespace, Name: object.Name, TenantID: object.Labels["kubebox.io/tenant"],
		OwnerID: owner, IdempotencyKey: object.Spec.IdempotencyKey, Template: object.Spec.Template,
		RuntimeClass: runtimeClassFromClassRef(object.Spec.ClassRef), TTLSeconds: int(object.Spec.TTLSeconds),
		DesiredState: object.Spec.DesiredState, Phase: ClaimPhase(object.Status.Phase),
		PodName: object.Labels["kubebox.io/pod-name"], SandboxID: object.Status.SandboxRef,
	}
	if object.DeletionTimestamp != nil {
		claim.DesiredState = "Deleted"
	}
	_, err := (&Reconciler{Pods: r.PodClient, Now: now}).Reconcile(claim)
	if err != nil {
		return reconcile.Result{}, err
	}
	if claim.Phase != ClaimDeleted && object.DeletionTimestamp == nil && !containsString(object.Finalizers, claimFinalizer) {
		object.Finalizers = append(object.Finalizers, claimFinalizer)
		if err := r.Update(ctx, &object); err != nil {
			return reconcile.Result{}, err
		}
	}
	object.Status.Phase = string(claim.Phase)
	object.Status.SandboxRef = claim.SandboxID
	object.Status.ProxyEndpoint = claim.ProxyEndpoint
	if claim.ExpiresAt.IsZero() {
		object.Status.ExpiresAt = nil
	} else {
		expires := metav1.NewTime(claim.ExpiresAt)
		object.Status.ExpiresAt = &expires
	}
	if claim.Phase == ClaimDeleted && containsString(object.Finalizers, claimFinalizer) {
		object.Finalizers = removeString(object.Finalizers, claimFinalizer)
		if err := r.Update(ctx, &object); err != nil {
			return reconcile.Result{}, err
		}
	}
	if err := r.Status().Update(ctx, &object); err != nil {
		return reconcile.Result{}, err
	}
	if claim.Phase == ClaimProvisioning {
		return reconcile.Result{RequeueAfter: 500 * time.Millisecond}, nil
	}
	return reconcile.Result{}, nil
}

func (r *SandboxClaimReconciler) SetupWithManager(mgr manager.Manager) error {
	if r.Client == nil {
		r.Client = mgr.GetClient()
	}
	return builder.ControllerManagedBy(mgr).For(&kubeapi.SandboxClaim{}).Complete(r)
}

func runtimeClassFromClassRef(classRef string) string {
	if classRef == "" {
		return "gvisor"
	}
	if classRef == "runc" || classRef == "gvisor" || classRef == "kata" {
		return classRef
	}
	return "gvisor"
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func removeString(values []string, wanted string) []string {
	out := values[:0]
	for _, value := range values {
		if value != wanted {
			out = append(out, value)
		}
	}
	return out
}

var _ reconcile.Reconciler = (*SandboxClaimReconciler)(nil)
var _ = errors.Is
