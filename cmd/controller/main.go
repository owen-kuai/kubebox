package main

import (
	"flag"
	"os"

	"github.com/owen-kuai/kubebox/internal/dataplane"
	"github.com/owen-kuai/kubebox/internal/operator"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(operator.AddToScheme(scheme))
}

func main() {
	var metricsAddr string
	var probeAddr string
	var enableLeaderElection bool
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "address to bind metrics server")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "address to bind health probe server")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false, "enable leader election (requires RBAC)")

	logger := zap.New(zap.UseFlagOptions(&zap.Options{}))
	ctrl.SetLogger(logger)
	flag.Parse()

	manager, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "kubebox-controller.sandbox.kubebox.io",
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// Optional envd-proxy route registration. When KUBEBOX_ENVD_PROXY_URL and
	// KUBEBOX_ADMIN_SECRET are set, the controller keeps envd-proxy's route
	// table in sync as sandboxes become Ready / are deleted.
	var registrar dataplane.RouteRegistrar
	if proxyURL := os.Getenv("KUBEBOX_ENVD_PROXY_URL"); proxyURL != "" {
		client, err := dataplane.NewRouteClient(proxyURL, os.Getenv("KUBEBOX_ADMIN_SECRET"))
		if err != nil {
			setupLog.Error(err, "unable to configure route client")
			os.Exit(1)
		}
		registrar = client
		setupLog.Info("route registration enabled", "proxy", proxyURL)
	}

	if err := operator.SetupManager(manager, registrar); err != nil {
		setupLog.Error(err, "unable to set up controller")
		os.Exit(1)
	}

	if err := manager.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := manager.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting kubebox sandbox controller")
	if err := manager.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
