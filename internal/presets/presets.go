// Package presets is the Postgres-backed repository for named server-side
// conversion presets; Postgres is the system of record for preset content
// and version lifecycle.
package presets

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Preset scopes (mirror the CHECK constraint in the presets table).
const (
	ScopeSystem = "system"
	ScopeUser   = "user"
)

// Preset operations (mirror the CHECK constraint in the presets table). This
// phase (18-presets) only ever writes/reads OperationConvert; the other
// enum values (extract, archive, inspect, render) stay dormant (D-05).
const (
	OperationConvert = "convert"
)

// ErrNotFound is returned when a preset does not exist, is inactive, or is
// not visible to the requesting client (D-03: all three cases return this
// SAME error, with no distinguishable branch, to avoid leaking existence
// across the client boundary).
var ErrNotFound = errors.New("preset not found")

// ErrAlreadyExists is returned by Create when an active row for the same
// (scope, client_id, name) already exists (D-03/D-09) -- the REST layer maps
// this, via errors.Is, to a 409 on POST /v1/presets.
var ErrAlreadyExists = errors.New("preset already exists")

// Preset is a row of the presets table. ClientID is nil for scope=system
// rows (presets_scope_owner_chk enforces system rows never carry a
// client_id, and user rows always do).
type Preset struct {
	ID           uuid.UUID
	Name         string
	Version      int
	Scope        string
	ClientID     *uuid.UUID
	Operation    string
	TargetFormat string
	Options      map[string]any
	Description  string
	IsActive     bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
