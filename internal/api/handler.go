package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/siqiliu18/healthwatch/internal/metrics"
	"github.com/siqiliu18/healthwatch/internal/model"
	"github.com/siqiliu18/healthwatch/internal/store"
)

type Handler struct {
	store store.Store
	cache store.Cache // nil if Redis is not configured
}

func (h *Handler) RegisterCheck(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Endpoint == "" {
		writeError(w, http.StatusBadRequest, "endpoint required")
		return
	}

	check, err := h.store.Register(r.Context(), body.Endpoint)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusCreated, check)
}

func (h *Handler) GetCheck(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	check, err := h.store.GetCheck(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	var result *model.CheckResult
	if h.cache != nil {
		result, err = h.cache.GetLatestResult(r.Context(), id)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			result = nil // non-fatal: fall through to Postgres
		}
	}
	if result == nil {
		result, err = h.store.GetLatestResult(r.Context(), id)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"check":         check,
		"latest_result": result,
	})
}

func (h *Handler) DeleteCheck(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	err = h.store.DeleteCheck(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) ListChecks(w http.ResponseWriter, r *http.Request) {
	limit, offset := 20, 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}

	checks, err := h.store.ListChecks(r.Context(), limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"checks": checks})
}

func (h *Handler) TryCheck(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	if _, err = h.store.GetCheck(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	if err := h.store.EnqueueJob(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

func (h *Handler) QueueDepth(w http.ResponseWriter, r *http.Request) {
	n, err := h.store.PendingJobCount(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	metrics.QueueDepth.Set(float64(n))
	writeJSON(w, http.StatusOK, map[string]any{"pending": n})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
