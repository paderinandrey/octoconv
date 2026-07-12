// Command manage-presets is the operator CLI for provisioning and managing
// named server-side conversion presets: create, update (bump-on-update,
// D-04), list, show, and deactivate — both system-scoped (no --client-id)
// and client-scoped (--client-id) presets. There is deliberately no delete
// verb; deactivate sets is_active=false and preserves history. This CLI is
// the sole management surface for presets in v1.4 (no REST CRUD until v2,
// PRST-V2-01).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/apaderin/octoconv/internal/db"
	"github.com/apaderin/octoconv/internal/presets"
	"github.com/google/uuid"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}

	ctx := context.Background()
	pool, err := db.Connect(ctx)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	repo := presets.NewRepo(pool)

	switch os.Args[1] {
	case "create":
		runCreate(ctx, repo, os.Args[2:])
	case "update":
		runUpdate(ctx, repo, os.Args[2:])
	case "list":
		runList(ctx, repo, os.Args[2:])
	case "show":
		runShow(ctx, repo, os.Args[2:])
	case "deactivate":
		runDeactivate(ctx, repo, os.Args[2:])
	default:
		usage()
	}
}

// scopeAndClient derives (scope, clientID) from the --client-id flag value:
// absence means system scope (no client), presence means user scope keyed
// on the parsed uuid (D-10 — the DDL presets_scope_owner_chk CHECK enforces
// the invariant; this is the only place Go derives scope, purely from flag
// presence, never re-validated against the DB).
func scopeAndClient(clientIDFlag string) (scope string, clientID *uuid.UUID) {
	if clientIDFlag == "" {
		return presets.ScopeSystem, nil
	}
	id, err := uuid.Parse(clientIDFlag)
	if err != nil {
		log.Fatalf("invalid client id: %v", err)
	}
	return presets.ScopeUser, &id
}

// parseOpts parses the --opts flag value (JSON object text) into a
// map[string]any. An empty string is valid and yields a nil map (no opts).
func parseOpts(raw string) map[string]any {
	if raw == "" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		log.Fatalf("invalid --opts JSON: %v", err)
	}
	return m
}

func runCreate(ctx context.Context, repo *presets.Repo, args []string) {
	fs := flag.NewFlagSet("create", flag.ExitOnError)
	name := fs.String("name", "", "preset name (required)")
	target := fs.String("target", "", "target format (required)")
	clientID := fs.String("client-id", "", "client id (omit for a system-scoped preset)")
	opts := fs.String("opts", "", "conversion options as a JSON object (optional)")
	description := fs.String("description", "", "human-readable description (optional)")
	fs.Parse(args)

	if *name == "" || *target == "" {
		fs.Usage()
		usage()
	}

	options := parseOpts(*opts)
	// D-11: fail early for operators; D-06 re-validation at use time (engine-
	// scoped, in handleCreateJob) remains the real enforcement point.
	if err := presets.ValidateOptsJSON(options); err != nil {
		log.Fatalf("invalid opts: %v", err)
	}

	scope, cid := scopeAndClient(*clientID)

	id, version, err := repo.Create(ctx, presets.CreateParams{
		Name:         *name,
		Scope:        scope,
		ClientID:     cid,
		TargetFormat: *target,
		Options:      options,
		Description:  *description,
	})
	if err != nil {
		log.Fatalf("create preset: %v", err)
	}

	fmt.Println("preset id:", id)
	fmt.Println("scope:", scope)
	fmt.Println("version:", version)
}

func runUpdate(ctx context.Context, repo *presets.Repo, args []string) {
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	name := fs.String("name", "", "preset name (required)")
	target := fs.String("target", "", "target format (required)")
	clientID := fs.String("client-id", "", "client id (omit for a system-scoped preset)")
	opts := fs.String("opts", "", "conversion options as a JSON object (optional)")
	description := fs.String("description", "", "human-readable description (optional)")
	fs.Parse(args)

	if *name == "" || *target == "" {
		fs.Usage()
		usage()
	}

	options := parseOpts(*opts)
	if err := presets.ValidateOptsJSON(options); err != nil {
		log.Fatalf("invalid opts: %v", err)
	}

	scope, cid := scopeAndClient(*clientID)

	// Bump-on-update (D-04): Update inserts a new version and deactivates the
	// prior one; it never mutates an existing row's content.
	version, err := repo.Update(ctx, scope, cid, *name, *target, options, *description)
	if err != nil {
		if errors.Is(err, presets.ErrNotFound) {
			log.Fatalf("no such preset: %s (scope=%s)", *name, scope)
		}
		log.Fatalf("update preset: %v", err)
	}

	fmt.Println("new version:", version)
}

func runList(ctx context.Context, repo *presets.Repo, args []string) {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	clientID := fs.String("client-id", "", "client id (omit to list system-scoped presets)")
	all := fs.Bool("all", false, "include inactive versions")
	fs.Parse(args)

	scope, cid := scopeAndClient(*clientID)

	list, err := repo.List(ctx, scope, cid, *all)
	if err != nil {
		log.Fatalf("list presets: %v", err)
	}

	if len(list) == 0 {
		fmt.Println("no presets found")
		return
	}

	fmt.Printf("%-24s %-8s %-8s %-10s %-8s\n", "NAME", "VERSION", "SCOPE", "TARGET", "ACTIVE")
	for _, p := range list {
		fmt.Printf("%-24s %-8d %-8s %-10s %-8t\n", p.Name, p.Version, p.Scope, p.TargetFormat, p.IsActive)
	}
}

func runShow(ctx context.Context, repo *presets.Repo, args []string) {
	fs := flag.NewFlagSet("show", flag.ExitOnError)
	name := fs.String("name", "", "preset name (required)")
	clientID := fs.String("client-id", "", "client id (omit for a system-scoped preset)")
	fs.Parse(args)

	if *name == "" {
		fs.Usage()
		usage()
	}

	scope, cid := scopeAndClient(*clientID)

	p, err := repo.Get(ctx, scope, cid, *name)
	if err != nil {
		if errors.Is(err, presets.ErrNotFound) {
			log.Fatalf("no such preset: %s (scope=%s)", *name, scope)
		}
		log.Fatalf("get preset: %v", err)
	}

	optsJSON, err := json.MarshalIndent(p.Options, "", "  ")
	if err != nil {
		log.Fatalf("marshal options: %v", err)
	}

	fmt.Println("id:", p.ID)
	fmt.Println("name:", p.Name)
	fmt.Println("version:", p.Version)
	fmt.Println("scope:", p.Scope)
	fmt.Println("client_id:", p.ClientID)
	fmt.Println("operation:", p.Operation)
	fmt.Println("target_format:", p.TargetFormat)
	fmt.Println("description:", p.Description)
	fmt.Println("is_active:", p.IsActive)
	fmt.Println("created_at:", p.CreatedAt)
	fmt.Println("updated_at:", p.UpdatedAt)
	fmt.Println("options:", string(optsJSON))
}

func runDeactivate(ctx context.Context, repo *presets.Repo, args []string) {
	fs := flag.NewFlagSet("deactivate", flag.ExitOnError)
	name := fs.String("name", "", "preset name (required)")
	clientID := fs.String("client-id", "", "client id (omit for a system-scoped preset)")
	fs.Parse(args)

	if *name == "" {
		fs.Usage()
		usage()
	}

	scope, cid := scopeAndClient(*clientID)

	if err := repo.Deactivate(ctx, scope, cid, *name); err != nil {
		if errors.Is(err, presets.ErrNotFound) {
			log.Fatalf("no such preset: %s (scope=%s)", *name, scope)
		}
		log.Fatalf("deactivate preset: %v", err)
	}

	fmt.Println("deactivated:", *name, "scope:", scope)
}

func usage() {
	log.Fatalf("usage: manage-presets <create|update|list|show|deactivate> [flags]\n" +
		"  create --name <n> --target <fmt> [--client-id <uuid>] [--opts '<json>'] [--description <d>]\n" +
		"  update --name <n> --target <fmt> [--client-id <uuid>] [--opts '<json>'] [--description <d>]\n" +
		"  list [--client-id <uuid>] [--all]\n" +
		"  show --name <n> [--client-id <uuid>]\n" +
		"  deactivate --name <n> [--client-id <uuid>]")
}
