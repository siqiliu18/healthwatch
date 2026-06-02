package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/siqiliu18/healthwatch/internal/store"
)

func NewServer(s store.Store, cache store.Cache) http.Handler {
	h := &Handler{store: s, cache: cache}
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	r.Get("/metrics/queue-depth", h.QueueDepth)

	r.Route("/checks", func(r chi.Router) {
		r.Post("/", h.RegisterCheck)
		r.Get("/", h.ListChecks)
		r.Get("/{id}", h.GetCheck)
		r.Delete("/{id}", h.DeleteCheck)
		r.Post("/{id}/try", h.TryCheck)
	})

	return r
}
