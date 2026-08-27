package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/owen-kuai/kubebox/internal/persistence"
	"github.com/owen-kuai/kubebox/internal/sandbox"
)

func main() {
	gov, err := openGovernanceStore()
	if err != nil {
		log.Fatalf("open governance store: %v", err)
	}
	if closer, ok := gov.(interface{ Close() error }); ok {
		defer func() { _ = closer.Close() }()
	}

	store, err := sandbox.NewStoreWithGovernance(gov)
	if err != nil {
		log.Fatal(err)
	}
	if err := store.SetQuota("default", "local-dev", 50); err != nil {
		log.Fatal(err)
	}
	addr := os.Getenv("KUBEBOX_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	log.Printf("kubebox MVP control plane listening on %s", addr)
	if err := http.ListenAndServe(addr, sandbox.NewHandler(store)); err != nil {
		log.Fatal(err)
	}
}

// openGovernanceStore selects the durable governance boundary:
//
//   - KUBEBOX_DATABASE_URL unset  -> in-memory store (dev / single-node default).
//   - KUBEBOX_DATABASE_URL set    -> real Postgres/MySQL store, migrated on boot.
//
// The in-memory store keeps the process runnable without external dependencies;
// setting the URL swaps in SQLStore (with advisory-lock migration) for durable
// quota/allocation/idempotency semantics.
func openGovernanceStore() (persistence.GovernanceStore, error) {
	dsn := os.Getenv("KUBEBOX_DATABASE_URL")
	if dsn == "" {
		return persistence.NewMemoryGovernanceStore(), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return persistence.OpenSQLStore(ctx, dsn)
}
