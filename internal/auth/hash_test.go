package auth

import (
	"regexp"
	"strings"
	"testing"
)

var hexDigestRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

func TestGenerateKeyUnique(t *testing.T) {
	a, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	b, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	if a == b {
		t.Fatalf("expected two GenerateKey calls to differ, got identical values %q", a)
	}
	if len(a) < 40 || len(a) > 46 {
		t.Fatalf("expected ~43 char base64url key, got %d chars: %q", len(a), a)
	}
}

func TestHashKeyDeterministic(t *testing.T) {
	salt := []byte("test-salt")
	raw := "raw-api-key-value"

	h1 := HashKey(salt, raw)
	h2 := HashKey(salt, raw)
	if h1 != h2 {
		t.Fatalf("expected same (salt, raw) to produce identical digest: %q != %q", h1, h2)
	}
}

func TestHashKeyDifferentSalt(t *testing.T) {
	raw := "raw-api-key-value"
	h1 := HashKey([]byte("salt-one"), raw)
	h2 := HashKey([]byte("salt-two"), raw)
	if h1 == h2 {
		t.Fatalf("expected different salt to produce different digest, got identical %q", h1)
	}
}

func TestHashKeyDifferentRaw(t *testing.T) {
	salt := []byte("test-salt")
	h1 := HashKey(salt, "raw-one")
	h2 := HashKey(salt, "raw-two")
	if h1 == h2 {
		t.Fatalf("expected different raw key to produce different digest, got identical %q", h1)
	}
}

func TestHashKeyOutputFormat(t *testing.T) {
	salt := []byte("test-salt")
	raw := "raw-api-key-value"
	h := HashKey(salt, raw)

	if !hexDigestRE.MatchString(h) {
		t.Fatalf("expected 64-char lowercase hex digest, got %q", h)
	}
	if strings.Contains(h, raw) {
		t.Fatalf("digest must never contain the raw key substring: %q", h)
	}
}
