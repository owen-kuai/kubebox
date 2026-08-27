package dataplane

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHealthMonitorEvictsUnhealthyRoute(t *testing.T) {
	// Backend that never becomes healthy.
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer dead.Close()

	routes := NewRouteRegistry()
	if err := routes.Set("sbx-dead", mustParseURL(dead.URL)); err != nil {
		t.Fatal(err)
	}
	monitor := NewHealthMonitor(routes)
	monitor.FailThreshold = 3
	monitor.Interval = time.Hour

	for i := 0; i < 3; i++ {
		monitor.CheckOnce(context.Background())
	}
	if _, ok := routes.Get("sbx-dead"); ok {
		t.Fatal("unhealthy route was not evicted after threshold")
	}
}

func TestHealthMonitorKeepsHealthyRoute(t *testing.T) {
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer healthy.Close()

	routes := NewRouteRegistry()
	if err := routes.Set("sbx-ok", mustParseURL(healthy.URL)); err != nil {
		t.Fatal(err)
	}
	monitor := NewHealthMonitor(routes)
	monitor.FailThreshold = 2

	for i := 0; i < 5; i++ {
		monitor.CheckOnce(context.Background())
	}
	if _, ok := routes.Get("sbx-ok"); !ok {
		t.Fatal("healthy route was wrongly evicted")
	}
}

func TestHealthMonitorRecoversBeforeThreshold(t *testing.T) {
	failing := true
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if failing {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	routes := NewRouteRegistry()
	if err := routes.Set("sbx-flaky", mustParseURL(backend.URL)); err != nil {
		t.Fatal(err)
	}
	monitor := NewHealthMonitor(routes)
	monitor.FailThreshold = 3

	// Two failures, then recovery, then more checks.
	monitor.CheckOnce(context.Background())
	monitor.CheckOnce(context.Background())
	failing = false
	monitor.CheckOnce(context.Background())
	monitor.CheckOnce(context.Background())

	if _, ok := routes.Get("sbx-flaky"); !ok {
		t.Fatal("recovered route was wrongly evicted")
	}
}
