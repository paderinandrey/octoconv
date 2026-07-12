package presets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repo is the presets repository backed by a pgx pool.
type Repo struct {
	pool *pgxpool.Pool
}

// NewRepo wraps a pgx pool.
func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

// CreateParams describes a brand-new preset (always inserted at version 1).
// Operation is not exposed here: Create always writes OperationConvert
// (D-05 — this phase only activates the 'convert' operation).
type CreateParams struct {
	Name         string
	Scope        string
	ClientID     *uuid.UUID
	TargetFormat string
	Options      map[string]any
	Description  string
}

// Resolve is the scope-precedence lookup (D-02/D-03/D-05): a client-scoped
// preset shadows a system preset of the same name for its owning client;
// system presets are usable by any client. All filtering (scope ownership,
// is_active, operation) lives in the SQL WHERE clause — there is no
// post-lookup Go ownership branch, so the nonexistent/inactive/cross-client
// miss cases are indistinguishable and all return ErrNotFound (T-18-01).
//
// Resolve is a plain non-locking SELECT; its is_active predicate is exactly
// the cheap re-check callers perform again immediately before the job-row
// insert (TOCTOU mitigation lives at the caller, not here).
func (r *Repo) Resolve(ctx context.Context, clientID uuid.UUID, name string) (*Preset, error) {
	var p Preset
	var targetFormat *string
	var optsJSON []byte
	err := r.pool.QueryRow(ctx, `
		SELECT id, name, version, scope, client_id, operation, target_format, options
		FROM presets
		WHERE name = $2 AND is_active AND operation = 'convert'
		  AND ((scope='system' AND client_id IS NULL) OR (scope='user' AND client_id = $1))
		ORDER BY (scope='user') DESC, version DESC
		LIMIT 1`,
		clientID, name,
	).Scan(&p.ID, &p.Name, &p.Version, &p.Scope, &p.ClientID, &p.Operation, &targetFormat, &optsJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("resolve preset %q: %w", name, err)
	}
	p.TargetFormat = deref(targetFormat)
	if err := json.Unmarshal(optsJSON, &p.Options); err != nil {
		return nil, fmt.Errorf("unmarshal preset options: %w", err)
	}
	return &p, nil
}

// Create inserts a brand-new preset at version 1, operation defaulting to
// OperationConvert (D-05). It returns the generated id and version. The
// presets_scope_owner_chk DDL constraint enforces the scope/client_id
// invariant (system->NULL, user->non-NULL) rather than Go re-deriving it
// (T-18-03). If an active version of the same (scope, client_id, name)
// already exists, Create returns a plain error instructing the operator to
// use Update instead (versions are immutable, D-04).
func (r *Repo) Create(ctx context.Context, p CreateParams) (uuid.UUID, int, error) {
	optsJSON := []byte("{}")
	if p.Options != nil {
		var err error
		optsJSON, err = json.Marshal(p.Options)
		if err != nil {
			return uuid.Nil, 0, fmt.Errorf("marshal preset options: %w", err)
		}
	}

	var exists bool
	if err := r.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM presets WHERE scope = $1 AND name = $2 AND is_active
			AND (client_id = $3 OR ($3::uuid IS NULL AND client_id IS NULL)))`,
		p.Scope, p.Name, p.ClientID,
	).Scan(&exists); err != nil {
		return uuid.Nil, 0, fmt.Errorf("check existing preset: %w", err)
	}
	if exists {
		return uuid.Nil, 0, fmt.Errorf("active preset %q already exists for scope %q; use Update instead: %w", p.Name, p.Scope, ErrAlreadyExists)
	}

	const version = 1
	var id uuid.UUID
	if err := r.pool.QueryRow(ctx,
		`INSERT INTO presets (name, version, scope, client_id, operation, target_format, options, description)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
		p.Name, version, p.Scope, p.ClientID, OperationConvert, p.TargetFormat, optsJSON, p.Description,
	).Scan(&id); err != nil {
		return uuid.Nil, 0, fmt.Errorf("insert preset: %w", err)
	}
	return id, version, nil
}

// Update performs bump-on-update (D-04) in one transaction: it locks the
// current active row for (scope, clientID, name), deactivates it, and
// inserts a new row at version+1 with is_active=true. Returns ErrNotFound if
// no active row exists. Versions are immutable — Update never mutates an
// existing row's content, only its is_active flag.
func (r *Repo) Update(ctx context.Context, scope string, clientID *uuid.UUID, name, targetFormat string, options map[string]any, description string) (int, error) {
	optsJSON := []byte("{}")
	if options != nil {
		var err error
		optsJSON, err = json.Marshal(options)
		if err != nil {
			return 0, fmt.Errorf("marshal preset options: %w", err)
		}
	}

	var newVersion int
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		var currentVersion int
		err := tx.QueryRow(ctx,
			`SELECT version FROM presets
			 WHERE scope = $1 AND name = $2 AND is_active
			   AND (client_id = $3 OR ($3::uuid IS NULL AND client_id IS NULL))
			 FOR UPDATE`,
			scope, name, clientID,
		).Scan(&currentVersion)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock active preset: %w", err)
		}

		newVersion = currentVersion + 1

		if _, err := tx.Exec(ctx,
			`UPDATE presets SET is_active = false
			 WHERE scope = $1 AND name = $2 AND version = $3
			   AND (client_id = $4 OR ($4::uuid IS NULL AND client_id IS NULL))`,
			scope, name, currentVersion, clientID,
		); err != nil {
			return fmt.Errorf("deactivate prior preset version: %w", err)
		}

		if _, err := tx.Exec(ctx,
			`INSERT INTO presets (name, version, scope, client_id, operation, target_format, options, description, is_active)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, true)`,
			name, newVersion, scope, clientID, OperationConvert, targetFormat, optsJSON, description,
		); err != nil {
			return fmt.Errorf("insert new preset version: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return newVersion, nil
}

// Deactivate flips is_active=false on the current active row for (scope,
// clientID, name); no hard DELETE (D-04, mirrors the client key-rotation
// pattern). Returns ErrNotFound if no active row exists.
func (r *Repo) Deactivate(ctx context.Context, scope string, clientID *uuid.UUID, name string) error {
	return pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		var id uuid.UUID
		err := tx.QueryRow(ctx,
			`SELECT id FROM presets
			 WHERE scope = $1 AND name = $2 AND is_active
			   AND (client_id = $3 OR ($3::uuid IS NULL AND client_id IS NULL))
			 FOR UPDATE`,
			scope, name, clientID,
		).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock active preset: %w", err)
		}

		if _, err := tx.Exec(ctx, `UPDATE presets SET is_active = false WHERE id = $1`, id); err != nil {
			return fmt.Errorf("deactivate preset: %w", err)
		}
		return nil
	})
}

// List returns presets for (scope, clientID), ordered by name then version
// descending. When includeInactive is false only is_active rows are
// returned. Shaped for reuse by a future MCP list_presets tool (SEED-003) —
// keep this signature stable.
func (r *Repo) List(ctx context.Context, scope string, clientID *uuid.UUID, includeInactive bool) ([]Preset, error) {
	query := `
		SELECT id, name, version, scope, client_id, operation, target_format, options, description, is_active, created_at, updated_at
		FROM presets
		WHERE scope = $1 AND (client_id = $2 OR ($2::uuid IS NULL AND client_id IS NULL))`
	if !includeInactive {
		query += ` AND is_active`
	}
	query += ` ORDER BY name, version DESC`

	rows, err := r.pool.Query(ctx, query, scope, clientID)
	if err != nil {
		return nil, fmt.Errorf("list presets: %w", err)
	}
	defer rows.Close()

	var out []Preset
	for rows.Next() {
		var p Preset
		var targetFormat, description *string
		var optsJSON []byte
		if err := rows.Scan(&p.ID, &p.Name, &p.Version, &p.Scope, &p.ClientID, &p.Operation,
			&targetFormat, &optsJSON, &description, &p.IsActive, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan preset: %w", err)
		}
		p.TargetFormat = deref(targetFormat)
		p.Description = deref(description)
		if err := json.Unmarshal(optsJSON, &p.Options); err != nil {
			return nil, fmt.Errorf("unmarshal preset options: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Get returns the highest-version active row for (scope, clientID, name);
// ErrNotFound if none (including the cross-client case, mirroring Resolve's
// no-leak semantics).
func (r *Repo) Get(ctx context.Context, scope string, clientID *uuid.UUID, name string) (*Preset, error) {
	var p Preset
	var targetFormat, description *string
	var optsJSON []byte
	err := r.pool.QueryRow(ctx, `
		SELECT id, name, version, scope, client_id, operation, target_format, options, description, is_active, created_at, updated_at
		FROM presets
		WHERE scope = $1 AND name = $2 AND is_active
		  AND (client_id = $3 OR ($3::uuid IS NULL AND client_id IS NULL))
		ORDER BY version DESC
		LIMIT 1`,
		scope, name, clientID,
	).Scan(&p.ID, &p.Name, &p.Version, &p.Scope, &p.ClientID, &p.Operation,
		&targetFormat, &optsJSON, &description, &p.IsActive, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get preset %q: %w", name, err)
	}
	p.TargetFormat = deref(targetFormat)
	p.Description = deref(description)
	if err := json.Unmarshal(optsJSON, &p.Options); err != nil {
		return nil, fmt.Errorf("unmarshal preset options: %w", err)
	}
	return &p, nil
}

// ListForClient returns the merged effective view for clientID (D-09/D-10):
// the client's own user-scope rows PLUS every system-scope row whose name is
// NOT shadowed by an active user-scope row of the same name for that client
// -- mirroring Resolve's user-shadows-system precedence, entirely in SQL (no
// post-query Go ownership/shadow branch). includeInactive=false returns
// active-only rows (on both sides of the union); the shadow check itself
// always considers only ACTIVE user rows, since an inactive override must
// not suppress a system preset. Ordered by name, then scope (user before
// system), then version descending.
func (r *Repo) ListForClient(ctx context.Context, clientID *uuid.UUID, includeInactive bool) ([]Preset, error) {
	query := `
		SELECT p.id, p.name, p.version, p.scope, p.client_id, p.operation, p.target_format, p.options, p.description, p.is_active, p.created_at, p.updated_at
		FROM presets p
		WHERE (
			(p.scope = 'user' AND p.client_id = $1)
			OR (p.scope = 'system' AND p.client_id IS NULL
				AND NOT EXISTS (
					SELECT 1 FROM presets u
					WHERE u.scope = 'user' AND u.client_id = $1 AND u.name = p.name AND u.is_active
				))
		)`
	if !includeInactive {
		query += ` AND p.is_active`
	}
	query += ` ORDER BY p.name, (p.scope = 'user') DESC, p.version DESC`

	rows, err := r.pool.Query(ctx, query, clientID)
	if err != nil {
		return nil, fmt.Errorf("list presets for client: %w", err)
	}
	defer rows.Close()

	var out []Preset
	for rows.Next() {
		var p Preset
		var targetFormat, description *string
		var optsJSON []byte
		if err := rows.Scan(&p.ID, &p.Name, &p.Version, &p.Scope, &p.ClientID, &p.Operation,
			&targetFormat, &optsJSON, &description, &p.IsActive, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan preset: %w", err)
		}
		p.TargetFormat = deref(targetFormat)
		p.Description = deref(description)
		if err := json.Unmarshal(optsJSON, &p.Options); err != nil {
			return nil, fmt.Errorf("unmarshal preset options: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetForClient returns the single effective active preset for name applying
// the same shadow precedence as Resolve/ListForClient (user version wins
// over a same-name system preset), but returns the FULL column set (D-09)
// so the REST DTO has description/is_active/timestamps. ErrNotFound when
// neither a user nor a system row exists (or the only match is inactive).
func (r *Repo) GetForClient(ctx context.Context, clientID *uuid.UUID, name string) (*Preset, error) {
	var p Preset
	var targetFormat, description *string
	var optsJSON []byte
	err := r.pool.QueryRow(ctx, `
		SELECT id, name, version, scope, client_id, operation, target_format, options, description, is_active, created_at, updated_at
		FROM presets
		WHERE name = $2 AND is_active AND operation = 'convert'
		  AND ((scope='system' AND client_id IS NULL) OR (scope='user' AND client_id = $1))
		ORDER BY (scope='user') DESC, version DESC
		LIMIT 1`,
		clientID, name,
	).Scan(&p.ID, &p.Name, &p.Version, &p.Scope, &p.ClientID, &p.Operation,
		&targetFormat, &optsJSON, &description, &p.IsActive, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get preset for client %q: %w", name, err)
	}
	p.TargetFormat = deref(targetFormat)
	p.Description = deref(description)
	if err := json.Unmarshal(optsJSON, &p.Options); err != nil {
		return nil, fmt.Errorf("unmarshal preset options: %w", err)
	}
	return &p, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
