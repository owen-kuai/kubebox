package operator

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

type ClaimPhase string

const (
	ClaimPending      ClaimPhase = "Pending"
	ClaimProvisioning ClaimPhase = "Provisioning"
	ClaimReady        ClaimPhase = "Ready"
	ClaimDraining     ClaimPhase = "Draining"
	ClaimDeleted      ClaimPhase = "Deleted"
	ClaimFailed       ClaimPhase = "Failed"
)

type Claim struct {
	Namespace      string
	Name           string
	UID            string
	TenantID       string
	OwnerID        string
	IdempotencyKey string
	Template       string
	RuntimeClass   string
	TTLSeconds     int
	DesiredState   string
	Phase          ClaimPhase
	PodName        string
	PodIP          string
	SandboxID      string
	ProxyEndpoint  string
	ExpiresAt      time.Time
}

type Pod struct {
	Namespace    string
	Name         string
	RuntimeClass string
	SandboxID    string
	OwnerUID     string
	IP           string
	Labels       map[string]string
	Ready        bool
	Healthy      bool
	Deleted      bool
}

type PodClient interface {
	Get(namespace, name string) (Pod, error)
	Create(pod Pod) error
	Delete(namespace, name string) error
}

type Reconciler struct {
	Pods PodClient
	Now  func() time.Time
}

func (r *Reconciler) Reconcile(claim *Claim) (bool, error) {
	if claim == nil || r.Pods == nil {
		return false, ErrInvalidClaim
	}
	if r.Now == nil {
		r.Now = time.Now
	}
	now := r.Now().UTC()
	if claim.RuntimeClass == "" {
		claim.RuntimeClass = "gvisor"
	}
	if claim.TTLSeconds <= 0 {
		claim.TTLSeconds = 1800
	}
	if claim.DesiredState != "Deleted" && !claim.ExpiresAt.IsZero() && !now.Before(claim.ExpiresAt) {
		claim.DesiredState = "Deleted"
	}
	if claim.DesiredState == "Deleted" {
		return r.reconcileDelete(claim)
	}
	if claim.Phase == ClaimDeleted {
		return false, nil
	}
	claim.Phase = ClaimProvisioning
	claim.PodName = DeterministicPodName(claim.TenantID, claim.IdempotencyKey)

	pod, err := r.Pods.Get(claim.Namespace, claim.PodName)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return false, err
	}
	if errors.Is(err, ErrNotFound) {
		pod = Pod{
			Namespace: claim.Namespace, Name: claim.PodName, RuntimeClass: claim.RuntimeClass, SandboxID: claim.Name, OwnerUID: claim.UID,
			Labels: map[string]string{
				"kubebox.io/tenant":   claim.TenantID,
				"kubebox.io/owner":    claim.OwnerID,
				"kubebox.io/claim":    claim.Name,
				"kubebox.io/workload": "sandbox",
			},
		}
		if err := r.Pods.Create(pod); err != nil && !errors.Is(err, ErrAlreadyExists) {
			claim.Phase = ClaimFailed
			return false, err
		}
		// Re-read after Create/AlreadyExists so a leader handoff follows the
		// existing Pod instead of creating a second side effect.
		pod, err = r.Pods.Get(claim.Namespace, claim.PodName)
		if err != nil {
			return false, err
		}
	}
	if pod.Deleted {
		claim.Phase = ClaimFailed
		return false, errors.New("pod is deleted")
	}
	if !pod.Ready || !pod.Healthy {
		return true, nil
	}
	claim.Phase = ClaimReady
	claim.SandboxID = claim.Name
	claim.PodIP = pod.IP
	claim.ProxyEndpoint = "envd-proxy.sandbox-system.svc:8080"
	if claim.ExpiresAt.IsZero() {
		claim.ExpiresAt = now.Add(time.Duration(claim.TTLSeconds) * time.Second)
	}
	return false, nil
}

func (r *Reconciler) reconcileDelete(claim *Claim) (bool, error) {
	claim.Phase = ClaimDraining
	if claim.PodName == "" {
		claim.PodName = DeterministicPodName(claim.TenantID, claim.IdempotencyKey)
	}
	if err := r.Pods.Delete(claim.Namespace, claim.PodName); err != nil && !errors.Is(err, ErrNotFound) {
		return false, err
	}
	claim.Phase = ClaimDeleted
	claim.ProxyEndpoint = ""
	claim.ExpiresAt = time.Time{}
	return false, nil
}

func DeterministicPodName(tenantID, idempotencyKey string) string {
	h := sha256.Sum256([]byte(tenantID + "\x00" + idempotencyKey))
	return "sbx-" + hex.EncodeToString(h[:])[:20]
}

func ValidateRuntimeClass(runtimeClass string) error {
	switch runtimeClass {
	case "runc", "gvisor", "kata":
		return nil
	default:
		return fmt.Errorf("runtime class %q is not allowed", runtimeClass)
	}
}

var (
	ErrInvalidClaim  = errors.New("invalid sandbox claim")
	ErrNotFound      = errors.New("pod not found")
	ErrAlreadyExists = errors.New("pod already exists")
)

func normalizeName(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
