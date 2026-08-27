package operator

import (
	"github.com/owen-kuai/kubebox/internal/dataplane"
	"github.com/owen-kuai/kubebox/internal/kubeapi"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

func AddToScheme(scheme *runtime.Scheme) error {
	return kubeapi.AddToScheme(scheme)
}

// SetupManager registers the SandboxClaim controller. An optional registrar
// keeps envd-proxy's route table in sync; pass nil to run without data-plane
// route registration (e.g. unit tests or a control-plane-only deployment).
func SetupManager(mgr manager.Manager, registrar dataplane.RouteRegistrar) error {
	r := &SandboxClaimReconciler{
		Client:    mgr.GetClient(),
		PodClient: &KubePodClient{Client: mgr.GetClient()},
		Registrar: registrar,
	}
	return r.SetupWithManager(mgr)
}

func NewPodClient(c client.Client) PodClient { return &KubePodClient{Client: c} }
