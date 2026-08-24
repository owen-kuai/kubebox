package operator

import (
	"github.com/owen-kuai/kubebox/internal/kubeapi"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

func AddToScheme(scheme *runtime.Scheme) error {
	return kubeapi.AddToScheme(scheme)
}

func SetupManager(mgr manager.Manager) error {
	r := &SandboxClaimReconciler{Client: mgr.GetClient(), PodClient: &KubePodClient{Client: mgr.GetClient()}}
	return r.SetupWithManager(mgr)
}

func NewPodClient(c client.Client) PodClient { return &KubePodClient{Client: c} }
