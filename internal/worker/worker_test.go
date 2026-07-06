package worker

import (
	"errors"
	"fmt"
	"testing"

	"github.com/minio/minio-go/v7"
)

func TestIsTerminalStorageNoSuchKey(t *testing.T) {
	// internal/storage wraps every minio error via fmt.Errorf("...: %w", err);
	// isTerminal must unwrap through that chain to find the NoSuchKey code.
	raw := minio.ErrorResponse{Code: minio.NoSuchKey, Message: "The specified key does not exist."}
	wrapped := fmt.Errorf("download %q: %w", "uploads/x/0-in.png", raw)
	if !isTerminal(wrapped) {
		t.Fatal("expected isTerminal(NoSuchKey) = true")
	}
}

func TestIsTerminalNoConverter(t *testing.T) {
	err := fmt.Errorf("no converter for %s -> %s", "bmp", "webp")
	if !isTerminal(err) {
		t.Fatal("expected isTerminal(\"no converter for ...\") = true")
	}
}

func TestIsTerminalVipsSignatures(t *testing.T) {
	cases := []string{
		"convert: exit status 1: vips__file_read_signature: is not a known file format",
		"convert: exit status 1: Premature end of JPEG file",
		"convert: exit status 1: JPEG datastream contains no image",
	}
	for _, msg := range cases {
		if !isTerminal(errors.New(msg)) {
			t.Fatalf("expected isTerminal(%q) = true", msg)
		}
	}
}

func TestIsTerminalTransientDefault(t *testing.T) {
	cases := []error{
		errors.New("dial tcp: connection refused"),
		errors.New("context deadline exceeded"),
		fmt.Errorf("upload %q: %w", "results/x/0-out.webp", errors.New("connection reset by peer")),
		nil,
	}
	for _, err := range cases {
		if isTerminal(err) {
			t.Fatalf("expected isTerminal(%v) = false (broad-retry default, D-01)", err)
		}
	}
}
