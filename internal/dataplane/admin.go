package dataplane

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Admin exposes the internal route-management API used by the control plane to
// register/unregister sandbox -> envd routes as sandboxes are created/released.
// It is a trusted, control-plane-only surface, authenticated with a shared
// admin secret (constant-time compared) and never exposed on the public
// data-plane listener.
type Admin struct {
	Routes      *RouteRegistry
	adminSecret string
}

// NewAdmin builds the route-management handler. adminSecret must be non-empty.
func NewAdmin(routes *RouteRegistry, adminSecret string) *Admin {
	return &Admin{Routes: routes, adminSecret: adminSecret}
}

// ServeHTTP dispatches the internal endpoints by path.
func (a *Admin) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/healthz":
		a.handleHealth(w, r)
	case r.URL.Path == "/internal/v1/routes":
		a.handleRoutes(w, r)
	case strings.HasPrefix(r.URL.Path, "/internal/v1/routes/"):
		a.handleRouteInstance(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

func (a *Admin) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleRoutes handles POST (register) and GET (list) on the collection.
func (a *Admin) handleRoutes(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		a.handleRegister(w, r)
	case http.MethodGet:
		if !a.authorized(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		writeJSON(w, http.StatusOK, a.Routes.List())
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// handleRouteInstance handles DELETE /internal/v1/routes/{sandboxId}.
func (a *Admin) handleRouteInstance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !a.authorized(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	sandboxID := strings.TrimPrefix(r.URL.Path, "/internal/v1/routes/")
	if sandboxID == "" || strings.Contains(sandboxID, "/") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "sandboxId is required"})
		return
	}
	if !a.Routes.Unregister(sandboxID) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "route not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "unregistered"})
}

// handleRegister parses and validates the target before registering.
func (a *Admin) handleRegister(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var req struct {
		SandboxID string `json:"sandboxId"`
		Target    string `json:"target"`
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "read body"})
		return
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if req.SandboxID == "" || req.Target == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "sandboxId and target are required"})
		return
	}
	target, err := url.Parse(req.Target)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid target URL"})
		return
	}
	if err := a.Routes.Set(req.SandboxID, target); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"status": "registered"})
}

func (a *Admin) authorized(r *http.Request) bool {
	if a.adminSecret == "" {
		return false
	}
	provided := r.Header.Get("X-Kubebox-Admin-Token")
	if provided == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(a.adminSecret)) == 1
}
