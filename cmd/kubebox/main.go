package main

import (
	"log"
	"net/http"
	"os"

	"github.com/owen-kuai/kubebox/internal/sandbox"
)

func main() {
	store := sandbox.NewStore()
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
