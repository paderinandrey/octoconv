// Package e2e is the project's first true end-to-end suite: it drives the
// FULL document conversion pipeline over real HTTP against a live
// docker-compose stack (API + document-worker + Postgres/Redis/MinIO), per
// Phase 11 D-03/D-04/D-05. It is env-gated on E2E_BASE_URL and self-skips
// silently without it, so `go test ./...` stays green offline.
//
// Required environment for a live run (see docker-compose.e2e.yml):
//
//	E2E_BASE_URL      e.g. http://localhost:8090 — the running API
//	DATABASE_URL      e.g. postgres://octo:octo-pass@localhost:5434/octo_db
//	API_KEY_SALT      MUST equal the running API's API_KEY_SALT value
//
// Optional:
//
//	E2E_WEBHOOK_HOST        host containers use to reach this process
//	                        (default host.docker.internal)
//	E2E_S3_DIAL_ADDR        host:port to dial for the presigned download when
//	                        the URL's host (e.g. minio:9000) does not resolve
//	                        from the host running the test (e.g. 127.0.0.1:9100)
//	WEBHOOK_SIGNING_SECRET  when set, the webhook signature is HMAC-verified
//	                        against it, not just asserted non-empty
//
// Deliberate convention deviation: this suite uses t.Run subtests for the
// 6-format-pair table, unlike the rest of the codebase (which uses no
// subtests anywhere) — a called-out exception for this first E2E suite so a
// single format's failure is reported per-pair instead of aborting the table.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/apaderin/octoconv/internal/auth"
	"github.com/apaderin/octoconv/internal/clients"
	"github.com/apaderin/octoconv/internal/db"
)

// e2eConfig carries the per-run environment the helpers below need.
type e2eConfig struct {
	baseURL     string
	webhookHost string
}

// e2eSetup gates the suite on E2E_BASE_URL (self-skip when unset, D-03) and
// resolves the optional knobs. It must be the first call in every E2E test.
func e2eSetup(t *testing.T) e2eConfig {
	t.Helper()
	baseURL := os.Getenv("E2E_BASE_URL")
	if baseURL == "" {
		t.Skip("E2E_BASE_URL not set; skipping E2E test")
	}
	webhookHost := os.Getenv("E2E_WEBHOOK_HOST")
	if webhookHost == "" {
		// host-gateway address the compose override maps for worker containers;
		// NOT 127.0.0.1 — loopback callback_urls stay hard-blocked by the SSRF
		// guard regardless of the WEBHOOK_ALLOW_* opt-outs.
		webhookHost = "host.docker.internal"
	}
	return e2eConfig{baseURL: baseURL, webhookHost: webhookHost}
}

// provisionClient creates a real client row in the stack's live Postgres and
// returns the raw API key, replicating cmd/manage-clients' create sequence.
// API_KEY_SALT MUST match the running API's API_KEY_SALT: the API resolves
// keys by comparing HashKey(salt, raw) digests, so a salt mismatch makes
// every request 401 even though the client row exists.
func provisionClient(t *testing.T) string {
	t.Helper()
	salt := []byte(os.Getenv("API_KEY_SALT"))
	if len(salt) == 0 {
		t.Fatal("API_KEY_SALT must be set (and must equal the running API's value)")
	}

	ctx := context.Background()
	pool, err := db.Connect(ctx) // reads DATABASE_URL
	if err != nil {
		t.Fatalf("db.Connect: %v", err)
	}
	t.Cleanup(pool.Close)

	repo := clients.NewRepo(pool)
	raw, err := auth.GenerateKey()
	if err != nil {
		t.Fatalf("auth.GenerateKey: %v", err)
	}
	hash := auth.HashKey(salt, raw)
	name := "e2e-test-client-" + uuid.NewString()[:8]
	if _, err := repo.Create(ctx, name, hash); err != nil {
		t.Fatalf("provision client %q: %v", name, err)
	}
	return raw
}

// postJob uploads data as a multipart job (fields: file, target and, when
// non-empty, callback_url) to POST /v1/jobs, asserts 202, and returns the
// created job id.
func postJob(t *testing.T, baseURL, apiKey, filename string, data []byte, target, callbackURL string) string {
	t.Helper()

	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	fw, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write(data); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := w.WriteField("target", target); err != nil {
		t.Fatalf("write target field: %v", err)
	}
	if callbackURL != "" {
		if err := w.WriteField("callback_url", callbackURL); err != nil {
			t.Fatalf("write callback_url field: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, baseURL+"/v1/jobs", &b)
	if err != nil {
		t.Fatalf("build POST /v1/jobs request: %v", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "ApiKey "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/jobs: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST /v1/jobs (%s -> %s) status = %d, want 202; body=%s",
			filename, target, resp.StatusCode, body)
	}

	var out struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode create response: %v; body=%s", err, body)
	}
	if out.JobID == "" {
		t.Fatalf("create response missing job_id; body=%s", body)
	}
	return out.JobID
}

// pollUntilDone polls GET /v1/jobs/{id} on a ~2s interval until the job
// reaches a terminal status or timeout elapses. It fatals on "failed"
// (surfacing error_code/error_message) and on timeout; on "done" it returns
// the terminal response body. Use a generous timeout: LibreOffice's first
// conversion in a fresh container is cold-start slow.
func pollUntilDone(t *testing.T, baseURL, apiKey, jobID string, timeout time.Duration) map[string]any {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last map[string]any
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodGet, baseURL+"/v1/jobs/"+jobID, nil)
		if err != nil {
			t.Fatalf("build GET /v1/jobs/%s request: %v", jobID, err)
		}
		req.Header.Set("Authorization", "ApiKey "+apiKey)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET /v1/jobs/%s: %v", jobID, err)
		}
		var body map[string]any
		decodeErr := json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /v1/jobs/%s status = %d, want 200; body=%v", jobID, resp.StatusCode, body)
		}
		if decodeErr != nil {
			t.Fatalf("decode GET /v1/jobs/%s response: %v", jobID, decodeErr)
		}
		last = body

		switch body["status"] {
		case "done":
			return body
		case "failed":
			t.Fatalf("job %s failed: error_code=%v error_message=%v",
				jobID, body["error_code"], body["error_message"])
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("job %s did not reach a terminal state within %v (last=%v)", jobID, timeout, last)
	return nil
}

// webhookHit is one captured webhook delivery: the signature/timestamp
// headers plus the raw JSON body.
type webhookHit struct {
	signature string
	timestamp string
	body      []byte
}

// startWebhookReceiver starts an httptest server bound to ALL interfaces
// (0.0.0.0, not httptest's loopback default) so the compose containers can
// reach it via host-gateway, and returns a container-reachable callback URL
// built from E2E_WEBHOOK_HOST (never the server's own 127.0.0.1 URL — the
// SSRF guard hard-blocks loopback unconditionally). Every delivery is sent
// on the returned buffered channel.
func startWebhookReceiver(t *testing.T, webhookHost string) (string, <-chan webhookHit) {
	t.Helper()

	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("listen for webhook receiver: %v", err)
	}

	received := make(chan webhookHit, 4)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body", http.StatusInternalServerError)
			return
		}
		received <- webhookHit{
			signature: r.Header.Get("X-OctoConv-Signature"),
			timestamp: r.Header.Get("X-OctoConv-Timestamp"),
			body:      b,
		}
		w.WriteHeader(http.StatusOK)
	}))
	srv.Listener.Close() // discard the default loopback listener
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)

	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("split webhook listener addr: %v", err)
	}
	callbackURL := fmt.Sprintf("http://%s/webhook", net.JoinHostPort(webhookHost, port))
	return callbackURL, received
}

// downloadClient returns the HTTP client for fetching the presigned
// download_url. When E2E_S3_DIAL_ADDR is set, every dial is redirected to
// that address while the request URL (and Host header) stay untouched — the
// presigned V4 signature covers the Host header, so rewriting the URL's host
// (e.g. minio:9000 -> localhost:9100) would break the signature, but dialing
// a different address under the original Host does not.
func downloadClient() *http.Client {
	dialAddr := os.Getenv("E2E_S3_DIAL_ADDR")
	if dialAddr == "" {
		return http.DefaultClient
	}
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, network, dialAddr)
			},
		},
	}
}
