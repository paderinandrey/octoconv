// Package clients is the Postgres-backed repository for API clients and their
// hashed keys; Postgres is the system of record for key validity.
package clients

import "github.com/google/uuid"

// Client is a row of the clients table (subset auth needs).
type Client struct {
	ID   uuid.UUID
	Name string
}
