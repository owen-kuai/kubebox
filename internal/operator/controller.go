package operator

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/owen-kuai/kubebox/internal/dataplane"
	"github.com/owen-kuai/kubebox/internal/kubeapi"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	claimFinalizer = "sandbox.kubebox.io/finalizer"
	ownerLabel     = "kubebox.io/owner"
)

// +kubebuilder:rbac:groups=sandbox.kubebox.io,resources=sandboxclaims,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=sandbox.kubebox.io,resources=sandboxclaims/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=sandbox.kubebox.io,resources=sandboxclaims/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch;delete

type SandboxClaimReconciler struct {
	client.Client
	PodClient PodClient
	Registrar dataplane.RouteRegistrar
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
		Namespace: object.Namespace, Name: object.Name, UID: string(object.UID), TenantID: object.Labels["kubebox.io/tenant"],
		OwnerID: owner, IdempotencyKey: object.Spec.IdempotencyKey, Template: object.Spec.Template,
		RuntimeClass: runtimeClassFromClassRef(object.Spec.ClassRef), TTLSeconds: int(object.Spec.TTLSeconds),
		DesiredState: object.Spec.DesiredState, Phase: ClaimPhase(object.Status.Phase),
		PodName: object.Labels["kubebox.io/pod-name"], SandboxID: object.Status.SandboxRef,
	}
	if object.Status.ExpiresAt != nil {
		claim.ExpiresAt = object.Status.ExpiresAt.Time
	}
	if object.DeletionTimestamp != nil {
		claim.DesiredState = "Deleted"
	}
	_, err := (&Reconciler{Pods: r.PodClient, Now: now}).Reconcile(claim)
	if err != nil {
		return reconcile.Result{}, err
	}
	routeErr := r.syncRoute(ctx, claim)
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
	if err := r.Status().Update(ctx, &object); err != nil {
		return reconcile.Result{}, err
	}
	if routeErr != nil {
		return reconcile.Result{RequeueAfter: 5 * time.Second}, nil
	}
	if claim.Phase == ClaimDeleted && containsString(object.Finalizers, claimFinalizer) {
		object.Finalizers = removeString(object.Finalizers, claimFinalizer)
		if err := r.Update(ctx, &object); err != nil {
			return reconcile.Result{}, err
		}
	}
	if claim.Phase == ClaimProvisioning {
		return reconcile.Result{RequeueAfter: 500 * time.Millisecond}, nil
	}
	if claim.Phase == ClaimReady && !claim.ExpiresAt.IsZero() {
		delay := claim.ExpiresAt.Sub(now())
		if delay <= 0 {
			delay = time.Second
		} else if delay > 30*time.Second {
			delay = 30 * time.Second
		}
		return reconcile.Result{RequeueAfter: delay}, nil
	}
	return reconcile.Result{}, nil
}

func (r *SandboxClaimReconciler) SetupWithManager(mgr manager.Manager) error {
	if r.Client == nil {
		r.Client = mgr.GetClient()
	}
	return builder.ControllerManagedBy(mgr).For(&kubeapi.SandboxClaim{}).Owns(&corev1.Pod{}).Complete(r)
}

// syncRoute keeps envd-proxy's route table in sync with the claim lifecycle: a
// Ready sandbox gets its route registered, a deleted sandbox has it removed.
// Errors are returned so the caller can retry until the data-plane state is
// converged.
func (r *SandboxClaimReconciler) syncRoute(ctx context.Context, claim *Claim) error {
	if r.Registrar == nil {
		return nil
	}
	logger := log.FromContext(ctx)
	switch {
	case claim.Phase == ClaimReady:
		if claim.PodIP == "" {
			logger.Info("skipping route registration: pod IP not yet available", "sandbox", claim.SandboxID)
			return fmt.Errorf("pod IP not yet available")
		}
		target := fmt.Sprintf("http://%s:8080", claim.PodIP)
		if err := r.Registrar.Register(ctx, claim.SandboxID, target); err != nil {
			logger.Error(err, "failed to register route", "sandbox", claim.SandboxID, "target", target)
			return err
		} else {
			logger.Info("registered sandbox route", "sandbox", claim.SandboxID, "target", target)
		}
	case claim.Phase == ClaimDeleted:
		if claim.SandboxID == "" {
			return nil
		}
		if err := r.Registrar.Unregister(ctx, claim.SandboxID); err != nil {
			logger.Error(err, "failed to unregister route", "sandbox", claim.SandboxID)
			return err
		} else {
			logger.Info("unregistered sandbox route", "sandbox", claim.SandboxID)
		}
	}
	return nil
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
