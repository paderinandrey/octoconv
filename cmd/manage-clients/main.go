// Command manage-clients is the operator CLI for provisioning and rotating
// client API keys: create a client (mints a primary key), add a secondary
// key for zero-downtime rotation, or revoke a key slot.
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/apaderin/octoconv/internal/auth"
	"github.com/apaderin/octoconv/internal/clients"
	"github.com/apaderin/octoconv/internal/db"
	"github.com/google/uuid"
)

func main() {
	// Read the pepper once, fail fast if unset (same style as db.Connect's
	// DATABASE_URL check).
	salt := []byte(os.Getenv("API_KEY_SALT"))
	if len(salt) == 0 {
		log.Fatal("API_KEY_SALT must be set")
	}

	if len(os.Args) < 2 {
		usage()
	}

	ctx := context.Background()
	pool, err := db.Connect(ctx)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	repo := clients.NewRepo(pool)

	switch os.Args[1] {
	case "create":
		if len(os.Args) != 3 {
			usage()
		}
		name := os.Args[2]

		raw, err := auth.GenerateKey()
		if err != nil {
			log.Fatalf("generate key: %v", err)
		}
		hash := auth.HashKey(salt, raw)

		id, err := repo.Create(ctx, name, hash)
		if err != nil {
			log.Fatalf("create client: %v", err)
		}

		// This is the only place the raw key ever exists in cleartext
		// (D-03/D-07): print it exactly once via fmt.Println, never
		// log.*, so it never gets a log prefix or hits a log aggregator.
		fmt.Println("client id:", id)
		fmt.Println("api key (save now, shown once):", raw)

	case "add-key":
		if len(os.Args) != 3 {
			usage()
		}
		id, err := uuid.Parse(os.Args[2])
		if err != nil {
			log.Fatalf("invalid client id: %v", err)
		}

		raw, err := auth.GenerateKey()
		if err != nil {
			log.Fatalf("generate key: %v", err)
		}
		hash := auth.HashKey(salt, raw)

		if err := repo.AddSecondaryKey(ctx, id, hash); err != nil {
			log.Fatalf("add secondary key: %v", err)
		}

		fmt.Println("api key (save now, shown once):", raw)

	case "revoke":
		if len(os.Args) != 4 {
			usage()
		}
		id, err := uuid.Parse(os.Args[2])
		if err != nil {
			log.Fatalf("invalid client id: %v", err)
		}
		slot := os.Args[3]

		if err := repo.RevokeKey(ctx, id, slot); err != nil {
			log.Fatalf("revoke key: %v", err)
		}

		log.Println("revoked", slot, "key for client", id)

	default:
		usage()
	}
}

func usage() {
	log.Fatalf("usage: manage-clients <create <name>|add-key <client-id>|revoke <client-id> <primary|secondary>>")
}
