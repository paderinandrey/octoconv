package webhook

import (
	"regexp"
	"strings"
	"testing"
)

var hexDigestRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

func TestSignPayloadDeterministic(t *testing.T) {
	secret := []byte("test-secret")
	body := []byte(`{"job_id":"abc"}`)

	h1 := SignPayload(secret, 1000, body)
	h2 := SignPayload(secret, 1000, body)
	if h1 != h2 {
		t.Fatalf("expected same (secret, timestamp, body) to produce identical digest: %q != %q", h1, h2)
	}
}

func TestSignPayloadDifferentSecret(t *testing.T) {
	body := []byte(`{"job_id":"abc"}`)
	h1 := SignPayload([]byte("secret-one"), 1000, body)
	h2 := SignPayload([]byte("secret-two"), 1000, body)
	if h1 == h2 {
		t.Fatalf("expected different secret to produce different digest, got identical %q", h1)
	}
}

func TestSignPayloadDifferentTimestamp(t *testing.T) {
	secret := []byte("test-secret")
	body := []byte(`{"job_id":"abc"}`)
	h1 := SignPayload(secret, 1000, body)
	h2 := SignPayload(secret, 2000, body)
	if h1 == h2 {
		t.Fatalf("expected different timestamp to produce different digest, got identical %q", h1)
	}
}

func TestSignPayloadDifferentBody(t *testing.T) {
	secret := []byte("test-secret")
	h1 := SignPayload(secret, 1000, []byte(`{"job_id":"one"}`))
	h2 := SignPayload(secret, 1000, []byte(`{"job_id":"two"}`))
	if h1 == h2 {
		t.Fatalf("expected different body to produce different digest, got identical %q", h1)
	}
}

func TestSignPayloadOutputFormat(t *testing.T) {
	secret := []byte("test-secret")
	body := []byte(`{"job_id":"abc"}`)
	h := SignPayload(secret, 1000, body)

	if !hexDigestRE.MatchString(h) {
		t.Fatalf("expected 64-char lowercase hex digest, got %q", h)
	}
	if strings.Contains(h, string(body)) {
		t.Fatalf("digest must never contain the raw body substring: %q", h)
	}
}
