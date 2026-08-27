package dataplane

import (
	"context"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// HealthMonitor periodically probes every registered envd backend's /healthz
// and unregisters routes whose backend stays unhealthy across a configurable
// number of consecutive failures. This keeps the proxy from forwarding to dead
// sandboxes after they are released or crash.
type HealthMonitor struct {
	Routes        *RouteRegistry
	Client        *http.Client
	Interval      time.Duration
	FailThreshold int

	mu       sync.Mutex
	failures map[string]int
}

// NewHealthMonitor builds a monitor with sane defaults.
func NewHealthMonitor(routes *RouteRegistry) *HealthMonitor {
	return &HealthMonitor{
		Routes:        routes,
		Client:        &http.Client{Timeout: 3 * time.Second},
		Interval:      10 * time.Second,
		FailThreshold: 3,
		failures:      make(map[string]int),
	}
}

// Start runs the probe loop until ctx is cancelled.
func (m *HealthMonitor) Start(ctx context.Context) {
	ticker := time.NewTicker(m.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.CheckOnce(ctx)
		}
	}
}

// CheckOnce probes all registered backends once and unregisters those that have
// failed for FailThreshold consecutive checks. It returns the number of routes
// unregistered this pass (useful for tests and logging).
func (m *HealthMonitor) CheckOnce(ctx context.Context) int {
	routes := m.Routes.List()
	unregistered := 0
	for sandboxID, target := range routes {
		if m.probe(ctx, target) {
			m.resetFailures(sandboxID)
			continue
		}
		if m.recordFailure(sandboxID) >= m.FailThreshold {
			if m.Routes.Unregister(sandboxID) {
				unregistered++
				log.Printf("health monitor: unregistered unhealthy route %s (%s)", sandboxID, target.String())
			}
			m.resetFailures(sandboxID)
		}
	}
	return unregistered
}

func (m *HealthMonitor) probe(ctx context.Context, target *url.URL) bool {
	// /healthz is appended to the target's path. A backend is healthy only if
	// it answers 2xx on its health endpoint.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, joinHealthURL(target.String()), nil)
	if err != nil {
		return false
	}
	resp, err := m.Client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

func (m *HealthMonitor) recordFailure(sandboxID string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failures[sandboxID]++
	return m.failures[sandboxID]
}

func (m *HealthMonitor) resetFailures(sandboxID string) {
	m.mu.Lock()
	delete(m.failures, sandboxID)
	m.mu.Unlock()
}

func joinHealthURL(target string) string {
	return strings.TrimRight(target, "/") + "/healthz"
}
