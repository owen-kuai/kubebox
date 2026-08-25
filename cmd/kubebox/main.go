package main

import (
	"log"
	"net/http"
	"os"

	"github.com/owen-kuai/kubebox/internal/persistence"
	"github.com/owen-kuai/kubebox/internal/sandbox"
)

func main() {
	// Default to the in-memory governance boundary so the process always runs
	// against a durable-interface store. When KUBEBOX_DATABASE_URL is set with
	// a Postgres/MySQL driver, swap in a SQLStore backed by real tables.
	gov := persistence.NewMemoryGovernanceStore()
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
