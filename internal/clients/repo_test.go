// Package clients_test lives in the external test package (not package
// clients) specifically to avoid an import cycle: it needs internal/auth for
// GenerateKey/HashKey, and internal/auth in turn imports internal/clients
// (for the ClientResolver/Resolver types added in Plan 02). An in-package
// test file cannot import something that imports its own package back.
package clients_test

import (
	"context"
	"os"
	"testing"

	"github.com/apaderin/octoconv/internal/auth"
	"github.com/apaderin/octoconv/internal/clients"
	"github.com/apaderin/octoconv/internal/db"
	"github.com/google/uuid"
)

var testSalt = []byte("fixed-test-salt")

func newTestRepo(t *testing.T) *clients.Repo {
	t.Helper()
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx := context.Background()
	pool, err := db.Connect(ctx)
	if err != nil {
		t.Fatalf("db.Connect: %v", err)
	}
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	t.Cleanup(pool.Close)
	return clients.NewRepo(pool)
}

func TestCreateAndGetByKeyHash(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	raw, err := auth.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	hash := auth.HashKey(testSalt, raw)

	id, err := r.Create(ctx, "test-client-"+uuid.NewString(), hash)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := r.GetByKeyHash(ctx, hash)
	if err != nil {
		t.Fatalf("GetByKeyHash: %v", err)
	}
	if got.ID != id {
		t.Fatalf("GetByKeyHash returned id %s, want %s", got.ID, id)
	}
}

func TestGetByKeyHashUnknown(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	_, err := r.GetByKeyHash(ctx, auth.HashKey(testSalt, "no-such-key"))
	if err != clients.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestAddSecondaryKey(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	primaryRaw, _ := auth.GenerateKey()
	primaryHash := auth.HashKey(testSalt, primaryRaw)
	id, err := r.Create(ctx, "test-client-"+uuid.NewString(), primaryHash)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	secondaryRaw, _ := auth.GenerateKey()
	secondaryHash := auth.HashKey(testSalt, secondaryRaw)
	if err := r.AddSecondaryKey(ctx, id, secondaryHash); err != nil {
		t.Fatalf("AddSecondaryKey: %v", err)
	}

	got, err := r.GetByKeyHash(ctx, secondaryHash)
	if err != nil {
		t.Fatalf("GetByKeyHash(secondary): %v", err)
	}
	if got.ID != id {
		t.Fatalf("GetByKeyHash(secondary) returned id %s, want %s", got.ID, id)
	}

	// Primary key must still resolve too — both slots active simultaneously.
	gotPrimary, err := r.GetByKeyHash(ctx, primaryHash)
	if err != nil {
		t.Fatalf("GetByKeyHash(primary) after AddSecondaryKey: %v", err)
	}
	if gotPrimary.ID != id {
		t.Fatalf("GetByKeyHash(primary) returned id %s, want %s", gotPrimary.ID, id)
	}
}

func TestRevokeKey(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	primaryRaw, _ := auth.GenerateKey()
	primaryHash := auth.HashKey(testSalt, primaryRaw)
	id, err := r.Create(ctx, "test-client-"+uuid.NewString(), primaryHash)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	secondaryRaw, _ := auth.GenerateKey()
	secondaryHash := auth.HashKey(testSalt, secondaryRaw)
	if err := r.AddSecondaryKey(ctx, id, secondaryHash); err != nil {
		t.Fatalf("AddSecondaryKey: %v", err)
	}

	if err := r.RevokeKey(ctx, id, "primary"); err != nil {
		t.Fatalf("RevokeKey(primary): %v", err)
	}

	if _, err := r.GetByKeyHash(ctx, primaryHash); err != clients.ErrNotFound {
		t.Fatalf("expected primary key to be revoked (ErrNotFound), got %v", err)
	}

	// Secondary must remain active — independent revocation per slot (AUTH-05).
	got, err := r.GetByKeyHash(ctx, secondaryHash)
	if err != nil {
		t.Fatalf("GetByKeyHash(secondary) after revoking primary: %v", err)
	}
	if got.ID != id {
		t.Fatalf("GetByKeyHash(secondary) returned id %s, want %s", got.ID, id)
	}
}
