// Package convert defines the Converter abstraction, a registry of supported
// format pairs, and the concrete engine implementations (libvips for images).
package convert

import (
	"context"
	"sort"
	"strings"
)

// Engine-class identifiers (D-01/DEBT-02). This is the SINGLE compile-time
// source of truth for engine-class string values -- referenced by
// Converter.Engine implementations (LibvipsConverter, LibreOfficeConverter,
// ChromiumConverter), the API routing switch (internal/api/handlers.go), the
// reconciler recovery-routing switch (internal/reconciler/reconciler.go),
// and the queue-name constants (internal/queue/queue.go). No other file may
// hold a raw "image"/"document"/"html" engine-class literal.
const (
	EngineImage    = "image"
	EngineDocument = "document"
	EngineHTML     = "html"
)

// Pair is an ordered (source, target) format pair, e.g. {"png", "webp"}.
// Formats are always normalized (lowercase, canonical aliases).
type Pair struct {
	From string
	To   string
}

// Converter turns a file in one format into another by shelling out to an
// external engine. inPath and outPath are local filesystem paths; the output
// format is implied by outPath's extension.
type Converter interface {
	// Pairs reports the format pairs this converter can handle.
	Pairs() []Pair
	// Convert reads inPath and writes the converted result to outPath.
	Convert(ctx context.Context, inPath, outPath string, opts map[string]any) error
	// Engine reports the engine class this converter belongs to (e.g.
	// "image", "document") -- the single source of truth for engine-class
	// routing (D-01).
	Engine() string
}

// NormalizeFormat lowercases a format/extension and folds common aliases so the
// registry has a single canonical key per format.
func NormalizeFormat(f string) string {
	f = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(f), "."))
	switch f {
	case "jpeg":
		return "jpg"
	case "tif":
		return "tiff"
	case "htm":
		return "html"
	default:
		return f
	}
}

// Registry maps normalized format pairs to the converter that handles them.
type Registry struct {
	m map[Pair]Converter
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{m: make(map[Pair]Converter)}
}

// Register adds a converter for every pair it advertises. Later registrations
// override earlier ones for the same pair.
func (r *Registry) Register(c Converter) {
	for _, p := range c.Pairs() {
		r.m[Pair{From: NormalizeFormat(p.From), To: NormalizeFormat(p.To)}] = c
	}
}

// Lookup finds the converter for a (from, to) pair, normalizing inputs.
func (r *Registry) Lookup(from, to string) (Converter, bool) {
	c, ok := r.m[Pair{From: NormalizeFormat(from), To: NormalizeFormat(to)}]
	return c, ok
}

// Supports reports whether a (from, to) pair is convertible.
func (r *Registry) Supports(from, to string) bool {
	_, ok := r.Lookup(from, to)
	return ok
}

// EngineFor reports the engine class that handles a (from, to) pair (D-02),
// or ("", false) if the pair is unsupported.
func (r *Registry) EngineFor(from, to string) (string, bool) {
	c, ok := r.Lookup(from, to)
	if !ok {
		return "", false
	}
	return c.Engine(), true
}

// Classes returns every registered (from, to) pair grouped by engine class
// (D-06) -- the single registry-derived source GET /v1/formats walks. Each
// class's pairs are sorted deterministically (by From then To) so repeated
// calls and JSON encoding order stay stable; no engine/pair string literal
// exists anywhere else in the codebase (this is the only walk of r.m).
func (r *Registry) Classes() map[string][]Pair {
	out := make(map[string][]Pair)
	for pair, c := range r.m {
		class := c.Engine()
		out[class] = append(out[class], pair)
	}
	for class := range out {
		pairs := out[class]
		sort.Slice(pairs, func(i, j int) bool {
			if pairs[i].From != pairs[j].From {
				return pairs[i].From < pairs[j].From
			}
			return pairs[i].To < pairs[j].To
		})
		out[class] = pairs
	}
	return out
}

// Default is the process-wide registry populated by converters.go.
var Default = NewRegistry()
