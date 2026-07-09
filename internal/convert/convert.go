// Package convert defines the Converter abstraction, a registry of supported
// format pairs, and the concrete engine implementations (libvips for images).
package convert

import (
	"context"
	"strings"
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

// Default is the process-wide registry populated by converters.go.
var Default = NewRegistry()
