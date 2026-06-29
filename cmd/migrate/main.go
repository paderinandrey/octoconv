// Command migrate applies all pending database migrations and exits.
package main

import (
	"context"
	"log"

	"github.com/apaderin/octoconv/internal/db"
)

func main() {
	ctx := context.Background()
	pool, err := db.Connect(ctx)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	log.Println("migrations applied")
}
