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
