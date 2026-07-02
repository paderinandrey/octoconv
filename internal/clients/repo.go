package clients

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a client (or an active key digest) does not exist.
var ErrNotFound = errors.New("client not found")

// Repo is the clients repository backed by a pgx pool.
type Repo struct {
	pool *pgxpool.Pool
}

// NewRepo wraps a pgx pool.
func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

// Create inserts a new client with its primary key digest already set,
// returning the generated client id.
func (r *Repo) Create(ctx context.Context, name string, primaryKeyHash string) (uuid.UUID, error) {
	var id uuid.UUID
	if err := r.pool.QueryRow(ctx,
		`INSERT INTO clients (name, api_key_hash) VALUES ($1, $2) RETURNING id`,
		name, primaryKeyHash,
	).Scan(&id); err != nil {
		return uuid.Nil, fmt.Errorf("insert client: %w", err)
	}
	return id, nil
}

// GetByKeyHash is the per-request auth lookup: it resolves a client by a
// salted key digest, matching either active (non-revoked) key slot. This is
// the only comparison of key-derived material, and it always operates on
// salted SHA-256 digests, never raw keys.
func (r *Repo) GetByKeyHash(ctx context.Context, keyHash string) (*Client, error) {
	var c Client
	err := r.pool.QueryRow(ctx, `
		SELECT id, name FROM clients
		WHERE (api_key_hash = $1 AND primary_revoked_at IS NULL)
		   OR (api_key_hash_secondary = $1 AND secondary_revoked_at IS NULL)`,
		keyHash,
	).Scan(&c.ID, &c.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get client by key hash: %w", err)
	}
	return &c, nil
}

// AddSecondaryKey activates a second key slot for zero-downtime rotation
// (AUTH-05). It fails if the secondary slot is already populated with an
// active (non-revoked) key, enforcing the two-active-keys-max cap.
func (r *Repo) AddSecondaryKey(ctx context.Context, clientID uuid.UUID, keyHash string) error {
	return pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		var existingHash *string
		var revokedAt *time.Time
		if err := tx.QueryRow(ctx,
			`SELECT api_key_hash_secondary, secondary_revoked_at FROM clients WHERE id = $1 FOR UPDATE`,
			clientID,
		).Scan(&existingHash, &revokedAt); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("lock client: %w", err)
		}

		if existingHash != nil && revokedAt == nil {
			return fmt.Errorf("client %s already has an active secondary key", clientID)
		}

		if _, err := tx.Exec(ctx,
			`UPDATE clients SET api_key_hash_secondary = $2, secondary_revoked_at = NULL WHERE id = $1`,
			clientID, keyHash,
		); err != nil {
			return fmt.Errorf("add secondary key: %w", err)
		}
		return nil
	})
}

// RevokeKey marks the given key slot ("primary" or "secondary") inactive by
// stamping its revoked-at timestamp. The client row and its history are never
// deleted, so past job history tied to this client survives revocation (D-05).
func (r *Repo) RevokeKey(ctx context.Context, clientID uuid.UUID, slot string) error {
	var updateSQL string
	switch slot {
	case "primary":
		updateSQL = `UPDATE clients SET primary_revoked_at = now() WHERE id = $1`
	case "secondary":
		updateSQL = `UPDATE clients SET secondary_revoked_at = now() WHERE id = $1`
	default:
		return fmt.Errorf("invalid key slot %q: must be \"primary\" or \"secondary\"", slot)
	}

	return pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM clients WHERE id = $1 FOR UPDATE)`, clientID,
		).Scan(&exists); err != nil {
			return fmt.Errorf("lock client: %w", err)
		}
		if !exists {
			return ErrNotFound
		}

		if _, err := tx.Exec(ctx, updateSQL, clientID); err != nil {
			return fmt.Errorf("revoke %s key: %w", slot, err)
		}
		return nil
	})
}
