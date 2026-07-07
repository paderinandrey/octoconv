package storage

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestRoundTrip exercises upload -> download -> presigned GET against a live
// S3/MinIO endpoint. It is skipped unless S3_* env vars are configured.
func TestRoundTrip(t *testing.T) {
	if os.Getenv("S3_ENDPOINT") == "" {
		t.Skip("S3_ENDPOINT not set; skipping integration test")
	}

	ctx := context.Background()
	c, err := New(ctx)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	jobID := uuid.New()
	key := InputKey(jobID, 0, "hello.txt")
	want := []byte("hello octoconv")

	if err := c.Upload(ctx, key, bytes.NewReader(want), int64(len(want)), "text/plain"); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	rc, err := c.Download(ctx, key)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	got, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("content mismatch: got %q want %q", got, want)
	}

	url, err := c.PresignGet(ctx, key, time.Minute)
	if err != nil {
		t.Fatalf("PresignGet: %v", err)
	}
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET presigned: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("presigned GET status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !bytes.Equal(body, want) {
		t.Fatalf("presigned content mismatch: got %q want %q", body, want)
	}

	// Missing object should error.
	if _, err := c.Download(ctx, "uploads/does-not-exist"); err == nil {
		t.Fatal("Download of missing object: expected error, got nil")
	}
}

// TestLifecycleConfig exercises the pure lifecycleConfig builder against the
// behaviors locked in by D-10/D-11 and Pitfall 3 — no live MinIO needed.
func TestLifecycleConfig(t *testing.T) {
	t.Run("168h produces two 7-day Enabled rules on uploads/ and results/", func(t *testing.T) {
		cfg := lifecycleConfig(168 * time.Hour)
		if len(cfg.Rules) != 2 {
			t.Fatalf("len(Rules) = %d, want 2", len(cfg.Rules))
		}
		wantPrefixes := map[string]bool{"uploads/": false, "results/": false}
		for _, r := range cfg.Rules {
			if r.Status != "Enabled" {
				t.Errorf("rule %s Status = %q, want Enabled", r.ID, r.Status)
			}
			if r.Expiration.Days != 7 {
				t.Errorf("rule %s Expiration.Days = %d, want 7", r.ID, r.Expiration.Days)
			}
			if _, ok := wantPrefixes[r.RuleFilter.Prefix]; !ok {
				t.Errorf("unexpected rule prefix %q", r.RuleFilter.Prefix)
				continue
			}
			wantPrefixes[r.RuleFilter.Prefix] = true
		}
		for prefix, seen := range wantPrefixes {
			if !seen {
				t.Errorf("no rule found for prefix %q", prefix)
			}
		}
	})

	t.Run("sub-day TTL clamps to 1 day", func(t *testing.T) {
		cfg := lifecycleConfig(time.Hour)
		for _, r := range cfg.Rules {
			if r.Expiration.Days != 1 {
				t.Errorf("rule %s Expiration.Days = %d, want 1 (clamped)", r.ID, r.Expiration.Days)
			}
		}
	})

	t.Run("zero TTL clamps to 1 day, never 0", func(t *testing.T) {
		cfg := lifecycleConfig(0)
		for _, r := range cfg.Rules {
			if r.Expiration.Days != 1 {
				t.Errorf("rule %s Expiration.Days = %d, want 1 (clamped)", r.ID, r.Expiration.Days)
			}
		}
	})

	t.Run("rule IDs are distinct and non-empty", func(t *testing.T) {
		cfg := lifecycleConfig(168 * time.Hour)
		seen := map[string]bool{}
		for _, r := range cfg.Rules {
			if r.ID == "" {
				t.Error("rule ID is empty")
			}
			if seen[r.ID] {
				t.Errorf("duplicate rule ID %q", r.ID)
			}
			seen[r.ID] = true
		}
	})
}
