package dataplane

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// RouteRegistrar is the control-plane side of the route lifecycle. The
// operator calls Register when a sandbox Pod becomes Ready and Unregister when
// it is deleted, keeping envd-proxy's route table in sync with reality.
type RouteRegistrar interface {
	Register(ctx context.Context, sandboxID, target string) error
	Unregister(ctx context.Context, sandboxID string) error
}

// RouteClient is an HTTP client for the envd-proxy route-management API
// (the Admin surface). It authenticates with the shared admin secret.
type RouteClient struct {
	baseURL     string
	adminSecret string
	client      *http.Client
}

// NewRouteClient builds a client for baseURL (e.g.
// "http://envd-proxy.sandbox-system.svc:8080"). The base URL must not carry a
// trailing path.
func NewRouteClient(baseURL, adminSecret string) (*RouteClient, error) {
	if baseURL == "" || adminSecret == "" {
		return nil, fmt.Errorf("route client requires a base URL and admin secret")
	}
	if _, err := url.Parse(baseURL); err != nil {
		return nil, fmt.Errorf("invalid base URL: %w", err)
	}
	return &RouteClient{
		baseURL:     strings.TrimRight(baseURL, "/"),
		adminSecret: adminSecret,
		client:      &http.Client{Timeout: 5 * time.Second},
	}, nil
}

// Register registers (or updates) the sandbox -> target route.
func (c *RouteClient) Register(ctx context.Context, sandboxID, target string) error {
	body, err := json.Marshal(map[string]string{"sandboxId": sandboxID, "target": target})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/internal/v1/routes", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Kubebox-Admin-Token", c.adminSecret)
	return c.do(req)
}

// Unregister removes the sandbox route. A missing route is tolerated (no-op).
func (c *RouteClient) Unregister(ctx context.Context, sandboxID string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.baseURL+"/internal/v1/routes/"+url.PathEscape(sandboxID), nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Kubebox-Admin-Token", c.adminSecret)
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil // already gone
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return c.decodeError(resp)
}

func (c *RouteClient) do(req *http.Request) error {
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return c.decodeError(resp)
}

func (c *RouteClient) decodeError(resp *http.Response) error {
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	msg := strings.TrimSpace(string(data))
	if msg == "" {
		return fmt.Errorf("envd-proxy route API returned %s", resp.Status)
	}
	return fmt.Errorf("envd-proxy route API returned %s: %s", resp.Status, msg)
}

var _ RouteRegistrar = (*RouteClient)(nil)
