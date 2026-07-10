package worker

import (
	"context"
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

func TestIsTerminalLibreOfficeSignatures(t *testing.T) {
	cases := []string{
		"convert: libreoffice: output missing %PDF- magic bytes",
		"convert: libreoffice: output is empty",
		"convert: libreoffice: no export filter for docx -> mp3",
		"convert: libreoffice: output does not match expected container format odt",
		"convert: libreoffice: produced no output file for \"odt\": stat /work/in.odt: no such file or directory",
		"convert: libreoffice: pdf_profile requested for non-pdf target \"odt\"",
	}
	for _, msg := range cases {
		if !isTerminal(errors.New(msg)) {
			t.Fatalf("expected isTerminal(%q) = true", msg)
		}
	}
}

// TestIsTerminalTimeoutUnchanged asserts that a wrapped context.DeadlineExceeded
// (the shape a document/image engine timeout actually surfaces as) is STILL
// transient under the shared isTerminal — the image path (HandleImageConvert)
// must keep retrying its timeouts. Only the engine-scoped isDocumentTerminal
// diverges from this.
func TestIsTerminalTimeoutUnchanged(t *testing.T) {
	wrapped := fmt.Errorf("convert: %w", fmt.Errorf("soffice killed: %w", context.DeadlineExceeded))
	if isTerminal(wrapped) {
		t.Fatal("expected isTerminal(wrapped context.DeadlineExceeded) = false — image path must keep retrying timeouts")
	}
}

func TestIsDocumentTerminal(t *testing.T) {
	// A DOCUMENT_ENGINE_TIMEOUT expiry (exec.go's process-group-kill shape,
	// preserved through libreoffice.go and process()'s %w wrapping) IS
	// terminal for the document engine — DOC-08's deliberate divergence.
	timeoutErr := fmt.Errorf("convert: %w", fmt.Errorf("soffice killed: %w", context.DeadlineExceeded))
	if !isDocumentTerminal(timeoutErr) {
		t.Fatal("expected isDocumentTerminal(wrapped context.DeadlineExceeded) = true (DOC-08)")
	}

	// Delegates to isTerminal for every non-timeout signature.
	terminalCases := []error{
		fmt.Errorf("no converter for %s -> %s", "docx", "png"),
		errors.New("convert: libreoffice: output is empty"),
		errors.New("convert: libreoffice: output missing %PDF- magic bytes"),
		errors.New("convert: libreoffice: no export filter for docx -> mp3"),
		errors.New("convert: libreoffice: output does not match expected container format odt"),
		fmt.Errorf("download %q: %w", "uploads/x/0-in.docx", minio.ErrorResponse{Code: minio.NoSuchKey}),
	}
	for _, err := range terminalCases {
		if !isDocumentTerminal(err) {
			t.Fatalf("expected isDocumentTerminal(%v) = true", err)
		}
	}

	transientCases := []error{
		errors.New("dial tcp: connection refused"),
		nil,
	}
	for _, err := range transientCases {
		if isDocumentTerminal(err) {
			t.Fatalf("expected isDocumentTerminal(%v) = false", err)
		}
	}
}
