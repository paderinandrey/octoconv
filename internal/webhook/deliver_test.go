package webhook

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDeliverSuccess(t *testing.T) {
	body := []byte(`{"job_id":"abc","status":"done"}`)
	const signature = "deadbeef"
	const timestamp = int64(1700000000)

	var gotBody []byte
	var gotContentType, gotSignature, gotTimestamp string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		gotSignature = r.Header.Get("X-OctoConv-Signature")
		gotTimestamp = r.Header.Get("X-OctoConv-Timestamp")
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		gotBody = b
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := NewDeliverer()
	status, err := d.Deliver(context.Background(), srv.URL, body, timestamp, signature)
	if err != nil {
		t.Fatalf("Deliver: unexpected error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if gotContentType != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", gotContentType)
	}
	if gotSignature != signature {
		t.Fatalf("X-OctoConv-Signature = %q, want %q", gotSignature, signature)
	}
	if gotTimestamp != "1700000000" {
		t.Fatalf("X-OctoConv-Timestamp = %q, want 1700000000", gotTimestamp)
	}
	if !bytes.Equal(gotBody, body) {
		t.Fatalf("request body = %q, want %q", gotBody, body)
	}
}

func TestDeliverNon2xxIsFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	d := NewDeliverer()
	status, err := d.Deliver(context.Background(), srv.URL, []byte("{}"), 1700000000, "sig")
	if err == nil {
		t.Fatal("expected error for non-2xx response, got nil")
	}
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", status)
	}
}

func TestDeliverTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// A short-timeout Deliverer variant, constructed directly (in-package
	// test) rather than via NewDeliverer's fixed 10s D-08 timeout, to keep
	// this test fast and deterministic.
	d := &Deliverer{hc: &http.Client{Timeout: 20 * time.Millisecond}}
	_, err := d.Deliver(context.Background(), srv.URL, []byte("{}"), 1700000000, "sig")
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}
