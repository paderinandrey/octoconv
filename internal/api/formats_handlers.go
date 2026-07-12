package api

import (
	"net/http"

	"github.com/apaderin/octoconv/internal/convert"
)

// engineFormats is the JSON shape for a single engine class in the
// GET /v1/formats response: its supported (source, target) pairs, each
// rendered as a two-element [from, to] array.
type engineFormats struct {
	Pairs [][2]string `json:"pairs"`
}

// handleListFormats returns the registry-derived capability map (D-06):
// for each engine class, the (source, target) pairs convert.Default
// actually supports -- reshaped from convert.Default.Classes(), never a
// hardcoded pair list that can drift from the real registry.
func (s *Server) handleListFormats(w http.ResponseWriter, r *http.Request) {
	classes := convert.Default.Classes()

	engines := make(map[string]engineFormats, len(classes))
	for class, pairs := range classes {
		rendered := make([][2]string, 0, len(pairs))
		for _, p := range pairs {
			rendered = append(rendered, [2]string{p.From, p.To})
		}
		engines[class] = engineFormats{Pairs: rendered}
	}

	writeJSON(w, http.StatusOK, map[string]any{"engines": engines})
}
