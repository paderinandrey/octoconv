package api

import (
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/apaderin/octoconv/internal/auth"
)

// Routes builds the chi router for the API. /healthz stays outside the /v1
// group and requires no API key (D-09); every /v1 route requires a valid key
// via auth.Middleware — hard cutover (D-08), no unauthenticated path.
func (s *Server) Routes() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/healthz", s.handleHealth)
	r.Route("/v1", func(r chi.Router) {
		r.Use(auth.Middleware(s.resolver))
		r.Post("/jobs", s.handleCreateJob)
		r.Get("/jobs/{id}", s.handleGetJob)
	})
	return r
}
