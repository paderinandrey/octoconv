// Package auth provides pure, dependency-free helpers for generating and
// hashing API keys. Keys are high-entropy random tokens, not user-chosen
// passwords, so a fast salted digest (crypto/sha256) is the correct primitive
// here — not a slow password hash like bcrypt/argon2, which exist to defend
// against low-entropy user-chosen secrets.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// GenerateKey returns a new high-entropy raw API key: 32 bytes from
// crypto/rand, base64url-encoded (~43 chars, no padding). This is the only
// place a raw key is ever produced; callers must hash it with HashKey before
// persisting anything.
func GenerateKey() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate key: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// HashKey returns a deterministic, salted SHA-256 digest of raw as a 64-char
// lowercase hex string. salt is a server-side secret pepper supplied by the
// caller (never read from the environment here, per package convention) —
// wired from API_KEY_SALT by callers in cmd/manage-clients. Determinism lets
// Postgres look up a client by digest; the salt/pepper means a database-only
// leak yields non-reversible, non-precomputable digests.
func HashKey(salt []byte, raw string) string {
	h := sha256.New()
	h.Write(salt)
	h.Write([]byte(raw))
	return hex.EncodeToString(h.Sum(nil))
}
