package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/owen-kuai/kubebox/internal/dataplane"
)

// envd-proxy is the public data-plane edge. It validates short-lived scoped
// credentials, strips them, injects the sandbox identity + derived scopes, and
// reverse-proxies to the trusted envd HTTP gateway. A control-plane-only admin
// API registers/unregisters sandbox routes, and a background monitor evicts
// routes whose backends fail health checks.
func main() {
	tokenSecret := os.Getenv("KUBEBOX_TOKEN_SECRET")
	if len(tokenSecret) < 32 {
		log.Fatal("KUBEBOX_TOKEN_SECRET must be set and at least 32 bytes")
	}
	adminSecret := os.Getenv("KUBEBOX_ADMIN_SECRET")
	if adminSecret == "" {
		log.Fatal("KUBEBOX_ADMIN_SECRET must be set")
	}
	addr := os.Getenv("KUBEBOX_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	issuer, err := dataplane.NewTokenIssuer([]byte(tokenSecret))
	if err != nil {
		log.Fatalf("init token issuer: %v", err)
	}
	routes := dataplane.NewRouteRegistry()
	proxy := &dataplane.Proxy{Issuer: issuer, Routes: routes}
	admin := dataplane.NewAdmin(routes, adminSecret)
	monitor := dataplane.NewHealthMonitor(routes)

	// Route by path prefix: health + internal admin go to the admin handler,
	// everything else flows through the credential-validating proxy.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" || strings.HasPrefix(r.URL.Path, "/internal/") {
			admin.ServeHTTP(w, r)
			return
		}
		proxy.Handler().ServeHTTP(w, r)
	})

	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Background route health eviction.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go monitor.Start(ctx)

	// Serve until a shutdown signal.
	errCh := make(chan error, 1)
	go func() {
		log.Printf("envd-proxy listening on %s", addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	select {
	case sig := <-sigCh:
		log.Printf("received %s, shutting down", sig)
	case err := <-errCh:
		if err != nil {
			log.Fatalf("server error: %v", err)
		}
	}

	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = server.Shutdown(shutdownCtx)
	log.Println("envd-proxy stopped")
}
