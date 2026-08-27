package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/owen-kuai/kubebox/internal/envd"
	"google.golang.org/grpc"
)

// envd is the main process of the isolated sandbox container. It serves the
// same execution surface over two transports:
//
//   - gRPC (:50051) — control-plane / internal exec path (JSON codec, no protoc).
//   - HTTP gateway (:8080) — data-plane path reached by envd-proxy after the
//     public edge validates the short-lived scoped credential.
//
// Both share a single ProcessExecutor rooted at the sandbox directory, so
// commands and file IO never touch the host filesystem. The process is meant to
// run as PID 1 inside a gVisor/Kata container with a read-only rootfs + a
// writable /sandbox volume.
func main() {
	sandboxID := flag.String("sandbox-id", os.Getenv("KUBEBOX_SANDBOX_ID"), "sandbox identity (required)")
	root := flag.String("root", os.Getenv("KUBEBOX_SANDBOX_ROOT"), "sandbox root directory")
	grpcAddr := flag.String("grpc-addr", ":50051", "gRPC listen address")
	httpAddr := flag.String("http-addr", ":8080", "HTTP gateway listen address")
	flag.Parse()

	if *sandboxID == "" {
		log.Fatal("--sandbox-id (or KUBEBOX_SANDBOX_ID) is required")
	}
	if *root == "" {
		*root = "/sandbox"
	}
	if err := os.MkdirAll(*root, 0o755); err != nil {
		log.Fatalf("create sandbox root: %v", err)
	}

	executor, err := envd.NewProcessExecutor(*root)
	if err != nil {
		log.Fatalf("init process executor: %v", err)
	}

	// gRPC server for the control-plane / internal path.
	grpcServer := grpc.NewServer()
	envd.RegisterEnvdServer(grpcServer, &envd.Server{SandboxID: *sandboxID, Executor: executor})
	grpcLis, err := net.Listen("tcp", *grpcAddr)
	if err != nil {
		log.Fatalf("listen gRPC %s: %v", *grpcAddr, err)
	}

	// HTTP gateway for the data-plane proxy path.
	gateway := envd.NewHTTPGateway(*sandboxID, executor)
	httpServer := &http.Server{
		Addr:              *httpAddr,
		Handler:           gateway.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Run both servers; any fatal listen/start error terminates the process.
	errCh := make(chan error, 2)
	go func() {
		log.Printf("envd gRPC listening on %s", *grpcAddr)
		errCh <- grpcServer.Serve(grpcLis)
	}()
	go func() {
		log.Printf("envd HTTP gateway listening on %s", *httpAddr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	// Wait for shutdown signal or a fatal server error.
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

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	grpcServer.GracefulStop()
	_ = httpServer.Shutdown(shutdownCtx)
	log.Printf("envd %s stopped", *sandboxID)
}
