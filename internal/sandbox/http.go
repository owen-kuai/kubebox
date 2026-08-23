package sandbox

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

type Handler struct{ Store *Store }

func NewHandler(store *Store) http.Handler {
	h := &Handler{Store: store}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", h.health)
	mux.HandleFunc("/api/v1/sandboxes", h.create)
	mux.HandleFunc("/api/v1/sandboxes/", h.instance)
	return mux
}

func (h *Handler) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req CreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	owner := r.Header.Get("X-Owner-ID")
	key := r.Header.Get("Idempotency-Key")
	sb, err := h.Store.Create(owner, key, req)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, sb)
}

func (h *Handler) instance(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/sandboxes/")
	if id == "" || strings.Contains(id, "/") {
		writeError(w, http.StatusNotFound, "sandbox not found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		sb, err := h.Store.Get(id)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, sb)
	case http.MethodDelete:
		sb, err := h.Store.Drain(id)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, sb)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func writeStoreError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, ErrInvalidRequest):
		status = http.StatusBadRequest
	case errors.Is(err, ErrQuotaExceeded):
		status = http.StatusTooManyRequests
	case errors.Is(err, ErrIdempotencyConflict):
		status = http.StatusConflict
	case errors.Is(err, ErrIdempotencyPending):
		status = http.StatusConflict
	case errors.Is(err, ErrNotFound):
		status = http.StatusNotFound
	}
	writeError(w, status, err.Error())
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
