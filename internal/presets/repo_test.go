package presets

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/apaderin/octoconv/internal/db"
	"github.com/google/uuid"
)

func newTestRepo(t *testing.T) *Repo {
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
	return NewRepo(pool)
}

// createTestClient inserts a minimal clients row and returns its id, so
// integration tests can satisfy the presets.client_id foreign key without a
// cross-package import (mirrors internal/jobs/repo_test.go's helper).
func createTestClient(t *testing.T, r *Repo) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := r.pool.QueryRow(context.Background(), "INSERT INTO clients (name) VALUES ($1) RETURNING id", "presets-test-client").Scan(&id)
	if err != nil {
		t.Fatalf("create test client: %v", err)
	}
	return id
}

// uniqueName returns a uuid-suffixed preset name so tests never collide on
// the shared DB.
func uniqueName(t *testing.T, base string) string {
	t.Helper()
	return base + "-" + uuid.New().String()
}

// TestResolveShadowing covers D-02: a client-scoped preset shadows a
// same-name system preset for its owning client, while a different client
// with no override still resolves the system preset (Pitfall 10, both
// directions).
func TestResolveShadowing(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	clientA := createTestClient(t, r)
	clientB := createTestClient(t, r)
	name := uniqueName(t, "thumb")

	if _, _, err := r.Create(ctx, CreateParams{
		Name: name, Scope: ScopeSystem, TargetFormat: "webp",
	}); err != nil {
		t.Fatalf("Create system preset: %v", err)
	}
	if _, _, err := r.Create(ctx, CreateParams{
		Name: name, Scope: ScopeUser, ClientID: &clientA, TargetFormat: "png",
	}); err != nil {
		t.Fatalf("Create user preset for clientA: %v", err)
	}

	gotA, err := r.Resolve(ctx, clientA, name)
	if err != nil {
		t.Fatalf("Resolve for clientA: %v", err)
	}
	if gotA.TargetFormat != "png" || gotA.Scope != ScopeUser {
		t.Fatalf("clientA resolved %+v, want user preset (png)", gotA)
	}

	gotB, err := r.Resolve(ctx, clientB, name)
	if err != nil {
		t.Fatalf("Resolve for clientB: %v", err)
	}
	if gotB.TargetFormat != "webp" || gotB.Scope != ScopeSystem {
		t.Fatalf("clientB resolved %+v, want system preset (webp)", gotB)
	}
}

// TestResolveSystemOnlyFallback guards the client_id-filter-too-strict bug
// (Pitfall 10 inverse): a system-only preset (no user override anywhere)
// must resolve for ANY client.
func TestResolveSystemOnlyFallback(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	clientA := createTestClient(t, r)
	clientB := createTestClient(t, r)
	name := uniqueName(t, "systemonly")

	if _, _, err := r.Create(ctx, CreateParams{
		Name: name, Scope: ScopeSystem, TargetFormat: "avif",
	}); err != nil {
		t.Fatalf("Create system preset: %v", err)
	}

	for _, cid := range []uuid.UUID{clientA, clientB} {
		got, err := r.Resolve(ctx, cid, name)
		if err != nil {
			t.Fatalf("Resolve for client %s: %v", cid, err)
		}
		if got.TargetFormat != "avif" {
			t.Fatalf("client %s resolved %+v, want system preset (avif)", cid, got)
		}
	}
}

// TestResolveNoLeakNonexistent covers D-03: an unknown preset name returns
// ErrNotFound.
func TestResolveNoLeakNonexistent(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	clientA := createTestClient(t, r)

	_, err := r.Resolve(ctx, clientA, uniqueName(t, "does-not-exist"))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Resolve nonexistent = %v, want ErrNotFound", err)
	}
}

// TestResolveNoLeakInactive covers D-03: a deactivated preset returns
// ErrNotFound, indistinguishable from nonexistent.
func TestResolveNoLeakInactive(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	name := uniqueName(t, "deactivated")

	if _, _, err := r.Create(ctx, CreateParams{
		Name: name, Scope: ScopeSystem, TargetFormat: "png",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := r.Deactivate(ctx, ScopeSystem, nil, name); err != nil {
		t.Fatalf("Deactivate: %v", err)
	}

	clientA := createTestClient(t, r)
	_, err := r.Resolve(ctx, clientA, name)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Resolve deactivated = %v, want ErrNotFound", err)
	}
}

// TestResolveNoLeakCrossClient covers D-03: a user preset owned by client A
// requested by client B returns the SAME ErrNotFound as the other miss
// cases, with no distinguishable branch (T-18-01).
func TestResolveNoLeakCrossClient(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	clientA := createTestClient(t, r)
	clientB := createTestClient(t, r)
	name := uniqueName(t, "owned-by-a")

	if _, _, err := r.Create(ctx, CreateParams{
		Name: name, Scope: ScopeUser, ClientID: &clientA, TargetFormat: "png",
	}); err != nil {
		t.Fatalf("Create user preset for clientA: %v", err)
	}

	_, err := r.Resolve(ctx, clientB, name)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Resolve cross-client = %v, want ErrNotFound", err)
	}
}

// TestVersionDeterminism covers D-04/Pitfall 11: after Create then Update,
// Resolve returns the bumped version and exactly one active row exists for
// that (scope, client_id, name).
func TestVersionDeterminism(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	name := uniqueName(t, "versioned")

	if _, v, err := r.Create(ctx, CreateParams{
		Name: name, Scope: ScopeSystem, TargetFormat: "webp",
	}); err != nil || v != 1 {
		t.Fatalf("Create: version=%d err=%v, want version=1", v, err)
	}

	newVersion, err := r.Update(ctx, ScopeSystem, nil, name, "avif", nil, "bumped")
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if newVersion != 2 {
		t.Fatalf("Update returned version %d, want 2", newVersion)
	}

	got, err := r.Resolve(ctx, createTestClient(t, r), name)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Version != 2 || got.TargetFormat != "avif" {
		t.Fatalf("Resolve after update = %+v, want version 2 / avif", got)
	}

	var activeCount int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM presets WHERE scope = $1 AND name = $2 AND is_active`,
		ScopeSystem, name,
	).Scan(&activeCount); err != nil {
		t.Fatalf("count active rows: %v", err)
	}
	if activeCount != 1 {
		t.Fatalf("active row count = %d, want 1 (single-active-version invariant)", activeCount)
	}
}

// TestDeactivateNoHardDelete covers D-04: after Deactivate, Resolve returns
// ErrNotFound but the row still exists (no hard delete).
func TestDeactivateNoHardDelete(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	name := uniqueName(t, "todeactivate")

	if _, _, err := r.Create(ctx, CreateParams{
		Name: name, Scope: ScopeSystem, TargetFormat: "png",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := r.Deactivate(ctx, ScopeSystem, nil, name); err != nil {
		t.Fatalf("Deactivate: %v", err)
	}

	clientA := createTestClient(t, r)
	if _, err := r.Resolve(ctx, clientA, name); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Resolve after deactivate = %v, want ErrNotFound", err)
	}

	var count int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM presets WHERE scope = $1 AND name = $2`, ScopeSystem, name,
	).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("row count after deactivate = %d, want 1 (no hard delete)", count)
	}
}

// TestListActiveVsAll covers active-only vs includeInactive semantics for
// BOTH scopes, and asserts user-scope List excludes system rows and other
// clients' rows (scope + client_id isolation).
func TestListActiveVsAll(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	sysName1 := uniqueName(t, "sys1")
	sysName2 := uniqueName(t, "sys2")
	if _, _, err := r.Create(ctx, CreateParams{Name: sysName1, Scope: ScopeSystem, TargetFormat: "webp"}); err != nil {
		t.Fatalf("Create sys1: %v", err)
	}
	if _, _, err := r.Create(ctx, CreateParams{Name: sysName2, Scope: ScopeSystem, TargetFormat: "avif"}); err != nil {
		t.Fatalf("Create sys2: %v", err)
	}
	if err := r.Deactivate(ctx, ScopeSystem, nil, sysName2); err != nil {
		t.Fatalf("Deactivate sys2: %v", err)
	}

	activeOnly, err := r.List(ctx, ScopeSystem, nil, false)
	if err != nil {
		t.Fatalf("List system active-only: %v", err)
	}
	activeNames := map[string]bool{}
	for _, p := range activeOnly {
		activeNames[p.Name] = true
	}
	if !activeNames[sysName1] || activeNames[sysName2] {
		t.Fatalf("List system active-only = %+v, want sys1 present / sys2 absent", activeOnly)
	}

	all, err := r.List(ctx, ScopeSystem, nil, true)
	if err != nil {
		t.Fatalf("List system --all: %v", err)
	}
	allNames := map[string]bool{}
	for _, p := range all {
		allNames[p.Name] = true
	}
	if !allNames[sysName1] || !allNames[sysName2] {
		t.Fatalf("List system --all = %+v, want both sys1 and sys2 present", all)
	}

	// User scope: isolation across clients and from system rows.
	clientA := createTestClient(t, r)
	clientB := createTestClient(t, r)
	userNameA := uniqueName(t, "usera")
	userNameB := uniqueName(t, "userb")
	if _, _, err := r.Create(ctx, CreateParams{Name: userNameA, Scope: ScopeUser, ClientID: &clientA, TargetFormat: "png"}); err != nil {
		t.Fatalf("Create userNameA: %v", err)
	}
	if _, _, err := r.Create(ctx, CreateParams{Name: userNameB, Scope: ScopeUser, ClientID: &clientB, TargetFormat: "png"}); err != nil {
		t.Fatalf("Create userNameB: %v", err)
	}

	listA, err := r.List(ctx, ScopeUser, &clientA, false)
	if err != nil {
		t.Fatalf("List user clientA: %v", err)
	}
	sawUserNameA := false
	for _, p := range listA {
		if p.Name == userNameB {
			t.Fatalf("List for clientA leaked clientB's preset: %+v", listA)
		}
		if p.Name == sysName1 || p.Name == sysName2 {
			t.Fatalf("List for user scope leaked a system preset: %+v", listA)
		}
		if p.Name == userNameA {
			sawUserNameA = true
		}
	}
	if !sawUserNameA {
		t.Fatalf("List for clientA missing its own preset: %+v", listA)
	}
}

// TestGetFoundAndMisses asserts Get returns the right row + version,
// ErrNotFound on a nonexistent name, and ErrNotFound on a cross-client Get.
func TestGetFoundAndMisses(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	name := uniqueName(t, "getme")
	if _, _, err := r.Create(ctx, CreateParams{Name: name, Scope: ScopeSystem, TargetFormat: "webp"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := r.Update(ctx, ScopeSystem, nil, name, "avif", nil, "bumped"); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := r.Get(ctx, ScopeSystem, nil, name)
	if err != nil {
		t.Fatalf("Get found: %v", err)
	}
	if got.Version != 2 || got.TargetFormat != "avif" {
		t.Fatalf("Get found = %+v, want version 2 / avif", got)
	}

	if _, err := r.Get(ctx, ScopeSystem, nil, uniqueName(t, "no-such-name")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get nonexistent = %v, want ErrNotFound", err)
	}

	clientA := createTestClient(t, r)
	clientB := createTestClient(t, r)
	userPresetName := uniqueName(t, "clientaonly")
	if _, _, err := r.Create(ctx, CreateParams{Name: userPresetName, Scope: ScopeUser, ClientID: &clientA, TargetFormat: "png"}); err != nil {
		t.Fatalf("Create user preset for clientA: %v", err)
	}
	if _, err := r.Get(ctx, ScopeUser, &clientB, userPresetName); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get cross-client = %v, want ErrNotFound", err)
	}
}
