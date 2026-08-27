package dataplane

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

// CredentialEnvelope is a short-lived, scoped credential issued by the control plane.
// It is accepted only by envd-proxy and is never forwarded to envd.
type CredentialEnvelope struct {
	SandboxID string   `json:"sandboxId"`
	Scopes    []string `json:"scopes"`
	ExpiresAt int64    `json:"expiresAt"`
}

type tokenPayload struct {
	CredentialEnvelope
	IssuedAt int64 `json:"issuedAt"`
}

type TokenIssuer struct {
	secret []byte
	now    func() time.Time
}

func NewTokenIssuer(secret []byte) (*TokenIssuer, error) {
	if len(secret) < 32 {
		return nil, errors.New("token secret must be at least 32 bytes")
	}
	return &TokenIssuer{secret: append([]byte(nil), secret...), now: time.Now}, nil
}

func (i *TokenIssuer) Issue(sandboxID string, scopes []string, ttl time.Duration) (string, error) {
	if sandboxID == "" || len(scopes) == 0 || ttl <= 0 {
		return "", errors.New("invalid credential envelope")
	}
	now := i.now().UTC()
	payload, err := json.Marshal(tokenPayload{
		CredentialEnvelope: CredentialEnvelope{SandboxID: sandboxID, Scopes: append([]string(nil), scopes...), ExpiresAt: now.Add(ttl).Unix()},
		IssuedAt:           now.Unix(),
	})
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, i.secret)
	_, _ = mac.Write([]byte(encoded))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return encoded + "." + signature, nil
}

func (i *TokenIssuer) Validate(token, sandboxID, scope string) (CredentialEnvelope, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return CredentialEnvelope{}, errors.New("invalid credential envelope")
	}
	mac := hmac.New(sha256.New, i.secret)
	_, _ = mac.Write([]byte(parts[0]))
	provided, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(mac.Sum(nil), provided) {
		return CredentialEnvelope{}, errors.New("invalid credential signature")
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return CredentialEnvelope{}, errors.New("invalid credential payload")
	}
	var payload tokenPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return CredentialEnvelope{}, errors.New("invalid credential payload")
	}
	if payload.SandboxID != sandboxID || payload.ExpiresAt <= i.now().Unix() || !contains(payload.Scopes, scope) {
		return CredentialEnvelope{}, errors.New("credential rejected")
	}
	return payload.CredentialEnvelope, nil
}

type RouteRegistry struct {
	mu     sync.RWMutex
	routes map[string]*url.URL
}

func NewRouteRegistry() *RouteRegistry { return &RouteRegistry{routes: make(map[string]*url.URL)} }

func (r *RouteRegistry) Set(sandboxID string, target *url.URL) error {
	if sandboxID == "" || target == nil || target.Scheme != "http" && target.Scheme != "https" || target.Host == "" {
		return errors.New("invalid envd route")
	}
	cleanTarget := *target
	cleanTarget.User = nil
	cleanTarget.Fragment = ""
	r.mu.Lock()
	r.routes[sandboxID] = &cleanTarget
	r.mu.Unlock()
	return nil
}

func (r *RouteRegistry) Delete(sandboxID string) {
	r.mu.Lock()
	delete(r.routes, sandboxID)
	r.mu.Unlock()
}

func (r *RouteRegistry) Get(sandboxID string) (*url.URL, bool) {
	r.mu.RLock()
	target, ok := r.routes[sandboxID]
	if !ok {
		r.mu.RUnlock()
		return nil, false
	}
	copy := *target
	r.mu.RUnlock()
	return &copy, true
}

// List returns a snapshot of all registered sandbox -> target routes. It is
// used by the health monitor to probe backends and by admin introspection.
func (r *RouteRegistry) List() map[string]*url.URL {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]*url.URL, len(r.routes))
	for id, target := range r.routes {
		copy := *target
		out[id] = &copy
	}
	return out
}

// Unregister removes a route, reporting whether it existed. It is the health
// monitor's idempotent counterpart to Set.
func (r *RouteRegistry) Unregister(sandboxID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.routes[sandboxID]; !ok {
		return false
	}
	delete(r.routes, sandboxID)
	return true
}

type Proxy struct {
	Issuer *TokenIssuer
	Routes *RouteRegistry
}

func (p *Proxy) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sandboxID, backendPath, ok := parsePath(r.URL.Path)
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "sandbox route not found"})
			return
		}
		target, ok := p.Routes.Get(sandboxID)
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "proxy route not found"})
			return
		}
		scope := scopeFor(r.Method, backendPath)
		token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		envelope, err := p.Issuer.Validate(token, sandboxID, scope)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "credential rejected"})
			return
		}
		proxy := httputil.NewSingleHostReverseProxy(target)
		originalDirector := proxy.Director
		proxy.Director = func(req *http.Request) {
			originalDirector(req)
			req.URL.Path = joinPath(target.Path, backendPath)
			req.Header.Del("Authorization")
			req.Header.Set("X-Kubebox-Sandbox-ID", sandboxID)
			// Re-derive the granted scopes so the trusted envd gateway can
			// re-check them (defense in depth) without the client sending them.
			req.Header.Set("X-Kubebox-Scope", strings.Join(envelope.Scopes, ","))
		}
		proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "envd unavailable"})
		}
		proxy.ServeHTTP(w, r)
	})
}

func parsePath(path string) (sandboxID, backendPath string, ok bool) {
	const prefix = "/v1/sandboxes/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(path, prefix)
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], "/" + parts[1], true
}

func scopeFor(method, path string) string {
	switch {
	case strings.HasPrefix(path, "/commands"), strings.HasPrefix(path, "/process"), strings.HasPrefix(path, "/pty"):
		return "commands"
	case strings.HasPrefix(path, "/files"):
		return "files"
	case strings.HasPrefix(path, "/network"):
		return "network"
	case method == http.MethodGet && path == "/healthz":
		return "health"
	default:
		return "sandbox"
	}
}

func joinPath(base, suffix string) string {
	return strings.TrimRight("/"+strings.Trim(base, "/"), "/") + "/" + strings.TrimLeft(suffix, "/")
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
