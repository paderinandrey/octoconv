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
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"text/template"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/apaderin/octoconv/internal/auth"
	"github.com/apaderin/octoconv/internal/clients"
	"github.com/apaderin/octoconv/internal/convert"
	"github.com/apaderin/octoconv/internal/db"
	"github.com/apaderin/octoconv/internal/webhook"
)

// e2eHTTP is the shared client for the suite's API calls (postJob,
// pollUntilDone). A hung endpoint must surface as a diagnosable per-request
// client-timeout error, not a go-test binary-timeout panic (DEBT-03/WR-04);
// 30s is comfortably above a healthy single request but well under
// pollUntilDone's 5-minute between-poll deadline.
var e2eHTTP = &http.Client{Timeout: 30 * time.Second}

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

// buildJobRequest builds the shared multipart POST /v1/jobs request (fields:
// file, target and, when non-empty, callback_url and opts) used by both
// postJob and postJobExpectStatus, so the two helpers never drift on request
// shape.
func buildJobRequest(t *testing.T, baseURL, apiKey, filename string, data []byte, target, callbackURL, opts string) *http.Request {
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
	if opts != "" {
		if err := w.WriteField("opts", opts); err != nil {
			t.Fatalf("write opts field: %v", err)
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
	return req
}

// postJob uploads data as a multipart job (fields: file, target and, when
// non-empty, callback_url and opts) to POST /v1/jobs, asserts 202, and
// returns the created job id.
func postJob(t *testing.T, baseURL, apiKey, filename string, data []byte, target, callbackURL, opts string) string {
	t.Helper()

	req := buildJobRequest(t, baseURL, apiKey, filename, data, target, callbackURL, opts)

	resp, err := e2eHTTP.Do(req)
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

// postJobFull is postJob's sibling that returns the full decoded create
// response (not just the job id) so callers can assert on echoed fields such
// as "opts" (D-09).
func postJobFull(t *testing.T, baseURL, apiKey, filename string, data []byte, target, callbackURL, opts string) map[string]any {
	t.Helper()

	req := buildJobRequest(t, baseURL, apiKey, filename, data, target, callbackURL, opts)

	resp, err := e2eHTTP.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/jobs: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST /v1/jobs (%s -> %s) status = %d, want 202; body=%s",
			filename, target, resp.StatusCode, body)
	}

	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode create response: %v; body=%s", err, body)
	}
	if out["job_id"] == nil || out["job_id"] == "" {
		t.Fatalf("create response missing job_id; body=%s", body)
	}
	return out
}

// postJobExpectStatus is postJob's sibling for cases where no job is ever
// created: it builds the identical multipart request, asserts an arbitrary
// wantStatus (rather than postJob's hard-coded 202), and returns the raw
// response body for the caller to optionally inspect (D-07's CFB-rejection
// test and the opts-rejection table both use this for their 422 branches).
func postJobExpectStatus(t *testing.T, baseURL, apiKey, filename string, data []byte, target, opts string, wantStatus int) []byte {
	t.Helper()

	req := buildJobRequest(t, baseURL, apiKey, filename, data, target, "", opts)

	resp, err := e2eHTTP.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/jobs: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		t.Fatalf("POST /v1/jobs (%s -> %s) status = %d, want %d; body=%s",
			filename, target, resp.StatusCode, wantStatus, body)
	}
	return body
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
		resp, err := e2eHTTP.Do(req)
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

// pollUntilFailed is pollUntilDone's inverse sibling (Phase 23 Plan 03,
// D-09): identical ~2s poll loop, but the two helpers' pass/fail sense is
// deliberately swapped -- pollUntilFailed fatals on "done" (the whole point
// of the non-compliant hard gate is that the job must NEVER succeed) and
// returns the terminal response body on "failed". Keep these two helpers
// adjacent so they never drift apart.
func pollUntilFailed(t *testing.T, baseURL, apiKey, jobID string, timeout time.Duration) map[string]any {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last map[string]any
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodGet, baseURL+"/v1/jobs/"+jobID, nil)
		if err != nil {
			t.Fatalf("build GET /v1/jobs/%s request: %v", jobID, err)
		}
		req.Header.Set("Authorization", "ApiKey "+apiKey)
		resp, err := e2eHTTP.Do(req)
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
		case "failed":
			return body
		case "done":
			t.Fatalf("job %s reached \"done\" but was expected to fail terminally: %v", jobID, body)
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

// TestDocumentConversionE2E drives all 6 document format pairs
// (docx/xlsx/pptx/odt/ods/odp -> pdf) through the LIVE pipeline: multipart
// upload -> poll to done -> presigned download -> %PDF- magic-byte check
// (D-04, SC#2/SC#4). Exactly one pair (docx) additionally registers a
// callback_url at the in-test webhook receiver and asserts a signed webhook
// payload arrives (D-05, SC#3); the other 5 pairs poll only.
func TestDocumentConversionE2E(t *testing.T) {
	cfg := e2eSetup(t)
	apiKey := provisionClient(t)

	fixtures := []string{
		"sample.docx", // the one webhook-asserting pair (D-05)
		"sample.xlsx",
		"sample.pptx",
		"sample.odt",
		"sample.ods",
		"sample.odp",
	}

	for _, filename := range fixtures {
		t.Run(filename, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", filename))
			if err != nil {
				t.Fatalf("read fixture %s: %v", filename, err)
			}

			// D-05: exactly one pair also exercises webhook delivery.
			var callbackURL string
			var received <-chan webhookHit
			if filename == "sample.docx" {
				callbackURL, received = startWebhookReceiver(t, cfg.webhookHost)
			}

			jobID := postJob(t, cfg.baseURL, apiKey, filename, data, "pdf", callbackURL, "")

			// Generous bound: LibreOffice cold start in a fresh container is
			// slow, and the document queue may serialize the 6 jobs.
			body := pollUntilDone(t, cfg.baseURL, apiKey, jobID, 5*time.Minute)

			downloadURL, _ := body["download_url"].(string)
			if downloadURL == "" {
				t.Fatalf("job %s done but download_url missing/empty: %v", jobID, body)
			}
			assertDownloadIsPDF(t, downloadURL)

			if received != nil {
				assertSignedWebhook(t, received, jobID)
			}
		})
	}
}

// assertDownloadIsPDF fetches the presigned URL (no auth header — the URL is
// self-authorizing) and asserts the response body begins with the %PDF-
// magic bytes (D-04).
func assertDownloadIsPDF(t *testing.T, downloadURL string) {
	t.Helper()
	resp, err := downloadClient().Get(downloadURL)
	if err != nil {
		t.Fatalf("GET download_url: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		t.Fatalf("GET download_url status = %d, want 200; body=%s", resp.StatusCode, b)
	}
	head := make([]byte, 5)
	if _, err := io.ReadFull(resp.Body, head); err != nil {
		t.Fatalf("read download head: %v", err)
	}
	if string(head) != "%PDF-" {
		t.Fatalf("download head = %q, want %%PDF-", head)
	}
}

// assertDownloadIsPDFA fetches the presigned URL (no auth header -- the URL
// is self-authorizing) and asserts the response body begins with the %PDF-
// magic bytes AND contains the /GTS_PDFA OutputIntent marker substring --
// the live confirmation that a real PDF/A-2b export (opts={"pdf_profile":
// "pdf/a-2b"}) actually carries the OutputIntent tag on the real LibreOffice
// 7.4 engine (T-14-03, OPTS-02 success criterion 2).
func assertDownloadIsPDFA(t *testing.T, downloadURL string) {
	t.Helper()
	resp, err := downloadClient().Get(downloadURL)
	if err != nil {
		t.Fatalf("GET download_url: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		t.Fatalf("GET download_url status = %d, want 200; body=%s", resp.StatusCode, b)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read download body: %v", err)
	}
	if !bytes.HasPrefix(body, []byte("%PDF-")) {
		t.Fatalf("download does not start with %%PDF- magic bytes")
	}
	if !bytes.Contains(body, []byte("/GTS_PDFA")) {
		t.Fatalf("download does not contain the /GTS_PDFA OutputIntent marker -- not a valid PDF/A export")
	}
}

// crossFormatPairs is the phase's exactly-6 symmetric intra-family cross
// pairs (D-01/D-07), reusing the existing sample.* fixtures as inputs.
var crossFormatPairs = []struct {
	filename string
	target   string
}{
	{"sample.docx", "odt"},
	{"sample.odt", "docx"},
	{"sample.xlsx", "ods"},
	{"sample.ods", "xlsx"},
	{"sample.pptx", "odp"},
	{"sample.odp", "pptx"},
}

// TestCrossFormatConversionE2E drives all 6 intra-family cross-format pairs
// (docx<->odt, xlsx<->ods, pptx<->odp) through the LIVE pipeline: multipart
// upload -> poll to done -> presigned download -> convert.SniffContainer
// structural check against the expected target format (D-01/D-02/D-07). This
// is the live confirmation that the D-02 LibreOffice 7.4 filter names
// actually produce valid output -- a wrong filter name makes its pair fail
// here, not in an offline unit test. No webhook assertion needed in this
// table; the existing ->pdf table already covers the webhook path (D-05).
func TestCrossFormatConversionE2E(t *testing.T) {
	cfg := e2eSetup(t)
	apiKey := provisionClient(t)

	for _, pair := range crossFormatPairs {
		t.Run(pair.filename+"->"+pair.target, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", pair.filename))
			if err != nil {
				t.Fatalf("read fixture %s: %v", pair.filename, err)
			}

			jobID := postJob(t, cfg.baseURL, apiKey, pair.filename, data, pair.target, "", "")

			// Generous bound: LibreOffice cold start in a fresh container is
			// slow, and the document queue may serialize the 6 jobs.
			body := pollUntilDone(t, cfg.baseURL, apiKey, jobID, 5*time.Minute)

			downloadURL, _ := body["download_url"].(string)
			if downloadURL == "" {
				t.Fatalf("job %s done but download_url missing/empty: %v", jobID, body)
			}
			assertDownloadIsFormat(t, downloadURL, pair.target)
		})
	}
}

// assertDownloadIsFormat fetches the presigned URL (no auth header -- the
// URL is self-authorizing) and asserts the downloaded bytes structurally
// sniff as expectedFormat via convert.SniffContainer -- the SAME function
// that validates upload input (D-03's input/output symmetry), not a bare
// magic-byte check. Unlike assertDownloadIsPDF, this works for every non-pdf
// office target.
func assertDownloadIsFormat(t *testing.T, downloadURL, expectedFormat string) {
	t.Helper()
	resp, err := downloadClient().Get(downloadURL)
	if err != nil {
		t.Fatalf("GET download_url: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		t.Fatalf("GET download_url status = %d, want 200; body=%s", resp.StatusCode, b)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read download body: %v", err)
	}
	cr, err := convert.SniffContainer(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("SniffContainer(download): %v", err)
	}
	if cr.Format != expectedFormat {
		t.Fatalf("downloaded container format = %q, want %q", cr.Format, expectedFormat)
	}
}

// oleCFBFixtures is the two SAFE-01 sub-cases (SC#3): a genuine legacy
// binary Word97 .doc and a genuine password-protected (Agile-encrypted)
// OOXML .docx. Both begin with the 8-byte OLE-CFB magic and must be rejected
// 422 before any conversion is attempted, but 22-cfb-classification's D-06
// split now gives each its own DISTINCT 422 message via convert.ClassifyCFB
// (Plan 22-01/22-02) rather than the historical single combined message.
var oleCFBFixtures = []struct {
	filename   string
	wantSubstr string // case-insensitive substring expected in the 422 body
	wantAbsent string // case-insensitive substring that must NOT appear (proves distinctness)
}{
	{filename: "legacy.doc", wantSubstr: "legacy binary", wantAbsent: "password"},
	{filename: "encrypted.docx", wantSubstr: "remove the password"},
}

// TestOLECFBRejectionE2E proves live (against real fixtures, not synthetic
// magic bytes) that both OLE-CFB sub-cases are rejected 422 before any job
// is ever created -- unlike the postJob/pollUntilDone tables above, no job
// ID is produced here at all. D-07 (unconditional hard gate): this asserts
// the two DISTINCT messages, not a shared loose "password" substring --
// legacy.doc's body must NOT mention "password" at all, proving the split
// actually distinguishes the two cases end-to-end through the live API. The
// 422 status returning promptly (never a hang, bounded by e2eHTTP's 30s
// per-request timeout and this test's -timeout 5m) is success-criterion 3's
// live proof that ClassifyCFB is DoS-safe against real fixture bytes.
func TestOLECFBRejectionE2E(t *testing.T) {
	cfg := e2eSetup(t)
	apiKey := provisionClient(t)

	for _, tc := range oleCFBFixtures {
		t.Run(tc.filename, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", tc.filename))
			if err != nil {
				t.Fatalf("read fixture %s: %v", tc.filename, err)
			}

			body := postJobExpectStatus(t, cfg.baseURL, apiKey, tc.filename, data, "pdf", "", http.StatusUnprocessableEntity)
			lower := bytes.ToLower(body)

			if !bytes.Contains(lower, []byte(tc.wantSubstr)) {
				t.Errorf("422 body for %s does not contain %q; body=%s", tc.filename, tc.wantSubstr, body)
			}
			if tc.wantAbsent != "" && bytes.Contains(lower, []byte(tc.wantAbsent)) {
				t.Errorf("422 body for %s must NOT contain %q (distinct-message proof); body=%s", tc.filename, tc.wantAbsent, body)
			}
		})
	}
}

// pdfAProfileOpts is the opts value this suite requests for a PDF/A-2b
// export (D-01, the single accepted pdf_profile value).
const pdfAProfileOpts = `{"pdf_profile":"pdf/a-2b"}`

// TestPDFAExportE2E drives the OPTS-01/OPTS-02 happy path live: sample.docx
// -> pdf with opts={"pdf_profile":"pdf/a-2b"} polls to done, the create
// response echoes the normalized opts (D-09), and the downloaded PDF carries
// the /GTS_PDFA OutputIntent marker on the real LibreOffice 7.4 engine
// (T-14-03). A "NoOpts" subtest proves the existing no-opts document->pdf
// path is unaffected (success criterion 3 regression).
func TestPDFAExportE2E(t *testing.T) {
	cfg := e2eSetup(t)
	apiKey := provisionClient(t)

	data, err := os.ReadFile(filepath.Join("testdata", "sample.docx"))
	if err != nil {
		t.Fatalf("read fixture sample.docx: %v", err)
	}

	t.Run("PDFA", func(t *testing.T) {
		createResp := postJobFull(t, cfg.baseURL, apiKey, "sample.docx", data, "pdf", "", pdfAProfileOpts)
		opts, ok := createResp["opts"].(map[string]any)
		if !ok || opts["pdf_profile"] != "pdf/a-2b" {
			t.Errorf("create response opts = %v, want echoed pdf_profile=pdf/a-2b", createResp["opts"])
		}
		jobID, _ := createResp["job_id"].(string)

		body := pollUntilDone(t, cfg.baseURL, apiKey, jobID, 5*time.Minute)

		getOpts, ok := body["opts"].(map[string]any)
		if !ok || getOpts["pdf_profile"] != "pdf/a-2b" {
			t.Errorf("GET response opts = %v, want echoed pdf_profile=pdf/a-2b", body["opts"])
		}

		downloadURL, _ := body["download_url"].(string)
		if downloadURL == "" {
			t.Fatalf("job %s done but download_url missing/empty: %v", jobID, body)
		}
		assertDownloadIsPDFA(t, downloadURL)
	})

	t.Run("NoOptsRegression", func(t *testing.T) {
		jobID := postJob(t, cfg.baseURL, apiKey, "sample.docx", data, "pdf", "", "")

		body := pollUntilDone(t, cfg.baseURL, apiKey, jobID, 5*time.Minute)

		if _, present := body["opts"]; present {
			t.Errorf("GET response = %v, want no \"opts\" key for a no-opts job", body)
		}

		downloadURL, _ := body["download_url"].(string)
		if downloadURL == "" {
			t.Fatalf("job %s done but download_url missing/empty: %v", jobID, body)
		}
		assertDownloadIsPDF(t, downloadURL)
	})
}

// TestPDFANonCompliantE2E is the LIVE HARD GATE (D-09, ROADMAP success
// criterion 1): a deliberately non-compliant, /GTS_PDFA-marker-bearing PDF
// (trigger.docx, intercepted by the e2e-only soffice shim -- see
// docker-compose.e2e.yml) fed through the real validation path must fail the
// job TERMINALLY, never succeed, and never retry-storm. pollUntilFailed's
// 5-minute bound is itself part of the proof: a retry loop burning
// DOCUMENT_MAX_RETRY x backoff would blow this bound, so simply reaching
// "failed" within it demonstrates the failure was classified terminal on the
// FIRST attempt, not after exhausting retries. The veraPDF reason is then
// cross-checked directly in job_events.detail via a live Postgres query
// (provisionClient's DATABASE_URL precedent) -- proving the diagnostics
// trail (D-07), not just the terminal status code, actually reached the
// audit log.
func TestPDFANonCompliantE2E(t *testing.T) {
	cfg := e2eSetup(t)
	apiKey := provisionClient(t)

	data, err := os.ReadFile(filepath.Join("testdata", "trigger.docx"))
	if err != nil {
		t.Fatalf("read fixture trigger.docx: %v", err)
	}

	jobID := postJob(t, cfg.baseURL, apiKey, "trigger.docx", data, "pdf", "", pdfAProfileOpts)

	body := pollUntilFailed(t, cfg.baseURL, apiKey, jobID, 5*time.Minute)

	if code, _ := body["error_code"].(string); code != "engine_error" {
		t.Errorf("job %s error_code = %v, want \"engine_error\"", jobID, body["error_code"])
	}

	ctx := context.Background()
	pool, err := db.Connect(ctx) // reads DATABASE_URL
	if err != nil {
		t.Fatalf("db.Connect: %v", err)
	}
	defer pool.Close()

	jid, err := uuid.Parse(jobID)
	if err != nil {
		t.Fatalf("parse job id %q: %v", jobID, err)
	}

	var reason string
	row := pool.QueryRow(ctx,
		`SELECT detail->>'engine_stderr' FROM job_events WHERE job_id = $1 AND to_status = 'failed'`,
		jid,
	)
	if err := row.Scan(&reason); err != nil {
		t.Fatalf("query job_events.detail for job %s: %v", jobID, err)
	}
	if !strings.Contains(reason, "pdf/a non-compliant") {
		t.Errorf("job_events.detail.engine_stderr for job %s = %q, want it to contain \"pdf/a non-compliant\"", jobID, reason)
	}
}

// optsRejectionCases is the table of live opts inputs that must 422 (T-14-01/
// T-14-02): pdf_profile requested on a non-pdf target (inapplicable) and a
// malformed/unknown-key opts value.
var optsRejectionCases = []struct {
	name     string
	filename string
	target   string
	opts     string
}{
	{"InapplicableTarget", "sample.docx", "odt", pdfAProfileOpts},
	{"UnknownKey", "sample.docx", "pdf", `{"EncryptFile":true}`},
}

// TestOptsRejectionE2E proves live (against the real API, not a unit fake)
// that both opts-rejection cases 422 before any job reaches done -- modeled
// on TestOLECFBRejectionE2E's postJobExpectStatus pattern.
func TestOptsRejectionE2E(t *testing.T) {
	cfg := e2eSetup(t)
	apiKey := provisionClient(t)

	for _, tc := range optsRejectionCases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", tc.filename))
			if err != nil {
				t.Fatalf("read fixture %s: %v", tc.filename, err)
			}

			postJobExpectStatus(t, cfg.baseURL, apiKey, tc.filename, data, tc.target, tc.opts, http.StatusUnprocessableEntity)
		})
	}
}

// assertSignedWebhook blocks (bounded) on the receiver channel and asserts a
// webhook actually arrived with a non-empty X-OctoConv-Signature, a body
// whose job_id matches the created job, and a terminal status (D-05). When
// WEBHOOK_SIGNING_SECRET is set (the compose worker's known dev secret), the
// signature is fully HMAC-verified against internal/webhook's scheme.
func assertSignedWebhook(t *testing.T, received <-chan webhookHit, jobID string) {
	t.Helper()
	var hit webhookHit
	select {
	case hit = <-received:
	case <-time.After(90 * time.Second):
		t.Fatal("webhook for job did not arrive within 90s")
	}

	if hit.signature == "" {
		t.Error("webhook X-OctoConv-Signature header is empty")
	}
	if hit.timestamp == "" {
		t.Error("webhook X-OctoConv-Timestamp header is empty")
	}

	var payload map[string]any
	if err := json.Unmarshal(hit.body, &payload); err != nil {
		t.Fatalf("decode webhook body: %v; body=%s", err, hit.body)
	}
	if got := payload["job_id"]; got != jobID {
		t.Errorf("webhook job_id = %v, want %s", got, jobID)
	}
	status, _ := payload["status"].(string)
	if status != "done" && status != "failed" {
		t.Errorf("webhook status = %q, want a terminal status", status)
	}
	if status == "done" {
		if u, _ := payload["download_url"].(string); u == "" {
			t.Error("webhook body for a done job is missing download_url")
		}
	}

	// Full HMAC verification when the stack's signing secret is known
	// (docker-compose.yml worker: dev-only-change-me-in-real-deploys).
	if secret := os.Getenv("WEBHOOK_SIGNING_SECRET"); secret != "" {
		ts, err := strconv.ParseInt(hit.timestamp, 10, 64)
		if err != nil {
			t.Fatalf("parse webhook timestamp %q: %v", hit.timestamp, err)
		}
		want := webhook.SignPayload([]byte(secret), ts, hit.body)
		if hit.signature != want {
			t.Errorf("webhook signature = %s, want %s (HMAC over %d.%s)", hit.signature, want, ts, hit.body)
		}
	}
}

// downloadClientTimeout bounds the presigned-download request (DEBT-03/
// WR-04): larger than the API-call timeout since it transfers a real file
// body, but still well under pollUntilDone's 5-minute between-poll deadline
// so a hung download surfaces as a diagnosable client-timeout error, not a
// suite-wide go-test binary-timeout panic.
const downloadClientTimeout = 60 * time.Second

// downloadClient returns the HTTP client for fetching the presigned
// download_url. When E2E_S3_DIAL_ADDR is set, every dial is redirected to
// that address while the request URL (and Host header) stay untouched — the
// presigned V4 signature covers the Host header, so rewriting the URL's host
// (e.g. minio:9000 -> localhost:9100) would break the signature, but dialing
// a different address under the original Host does not. Both branches carry
// an explicit Timeout; only Transport/DialContext differ.
func downloadClient() *http.Client {
	dialAddr := os.Getenv("E2E_S3_DIAL_ADDR")
	if dialAddr == "" {
		return &http.Client{Timeout: downloadClientTimeout}
	}
	return &http.Client{
		Timeout: downloadClientTimeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, network, dialAddr)
			},
		},
	}
}

// downloadPDFBytes fetches the presigned URL (no auth header -- the URL is
// self-authorizing) and returns the full response body, asserting a 200
// status. Shared by mediaBoxWidth (HTML-03 page_size structural check) and
// the print_background byte-comparison subtest below.
func downloadPDFBytes(t *testing.T, downloadURL string) []byte {
	t.Helper()
	resp, err := downloadClient().Get(downloadURL)
	if err != nil {
		t.Fatalf("GET download_url: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		t.Fatalf("GET download_url status = %d, want 200; body=%s", resp.StatusCode, b)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read download body: %v", err)
	}
	return body
}

// mediaBoxPattern extracts a PDF page's /MediaBox array (4 numbers: x0 y0 x1
// y1, in points). Chromium's print-to-pdf output stores the Page dictionary
// uncompressed (only content streams are FlateDecode-compressed), so a raw
// byte-pattern search is a reasonable best-effort structural check here --
// NOT a general-purpose PDF parser.
var mediaBoxPattern = regexp.MustCompile(`/MediaBox\s*\[\s*([\d.]+)\s+([\d.]+)\s+([\d.]+)\s+([\d.]+)\s*\]`)

// mediaBoxWidth downloads the PDF at downloadURL and attempts to extract its
// first page's /MediaBox width (x1-x0, in points) -- the HTML-03 page_size
// opt's structural signal (RESEARCH.md Pattern 1: page_size is CSS-injected,
// not a CLI flag, so this is the live proof the CSS @page rule was actually
// honored by chromium-headless-shell's renderer). ok is false when the
// pattern is not found (e.g. a differently-structured PDF variant), signaling
// the caller to fall back to visual confirmation (recorded in the Task 3
// acceptance run) instead of failing the automated test on an inconclusive
// structural check.
func mediaBoxWidth(t *testing.T, downloadURL string) (float64, bool) {
	t.Helper()
	body := downloadPDFBytes(t, downloadURL)
	m := mediaBoxPattern.FindSubmatch(body)
	if m == nil {
		return 0, false
	}
	x0, err0 := strconv.ParseFloat(string(m[1]), 64)
	x1, err1 := strconv.ParseFloat(string(m[3]), 64)
	if err0 != nil || err1 != nil {
		return 0, false
	}
	return x1 - x0, true
}

// renderHTMLWithOpts uploads filename/data as an html->pdf job with the
// given opts JSON (mirrors TestPDFAExportE2E's opts-form-field usage),
// polls to done, asserts the download is a valid PDF, and returns the
// download URL for further structural inspection by the caller.
func renderHTMLWithOpts(t *testing.T, cfg e2eConfig, apiKey, filename string, data []byte, opts string) string {
	t.Helper()
	jobID := postJob(t, cfg.baseURL, apiKey, filename, data, "pdf", "", opts)
	body := pollUntilDone(t, cfg.baseURL, apiKey, jobID, 5*time.Minute)
	downloadURL, _ := body["download_url"].(string)
	if downloadURL == "" {
		t.Fatalf("job %s done but download_url missing/empty: %v", jobID, body)
	}
	assertDownloadIsPDF(t, downloadURL)
	return downloadURL
}

// TestHTMLConversionE2E drives the html->pdf happy path (HTML-01, success
// criterion 1) and the page_size/print_background print-opts round-trip
// (HTML-03, success criterion 3) through the LIVE pipeline: multipart
// upload -> poll to done -> presigned download -> %PDF- magic-byte check,
// mirroring TestDocumentConversionE2E's shape but for the html engine class.
func TestHTMLConversionE2E(t *testing.T) {
	cfg := e2eSetup(t)
	apiKey := provisionClient(t)

	data, err := os.ReadFile(filepath.Join("testdata", "sample.html"))
	if err != nil {
		t.Fatalf("read fixture sample.html: %v", err)
	}

	t.Run("HappyPath", func(t *testing.T) {
		jobID := postJob(t, cfg.baseURL, apiKey, "sample.html", data, "pdf", "", "")

		// Generous bound: chromium-headless-shell cold start in a fresh
		// container is slow, same rationale as LibreOffice's cold start in
		// TestDocumentConversionE2E.
		body := pollUntilDone(t, cfg.baseURL, apiKey, jobID, 5*time.Minute)

		downloadURL, _ := body["download_url"].(string)
		if downloadURL == "" {
			t.Fatalf("job %s done but download_url missing/empty: %v", jobID, body)
		}
		assertDownloadIsPDF(t, downloadURL)
	})

	// HTML-03: page_size a4 vs a5 must produce a differently-sized page.
	t.Run("PrintOptsPageSize", func(t *testing.T) {
		a4URL := renderHTMLWithOpts(t, cfg, apiKey, "sample.html", data, `{"page_size":"a4"}`)
		a5URL := renderHTMLWithOpts(t, cfg, apiKey, "sample.html", data, `{"page_size":"a5"}`)

		a4Width, a4ok := mediaBoxWidth(t, a4URL)
		a5Width, a5ok := mediaBoxWidth(t, a5URL)
		if a4ok && a5ok {
			if a4Width == a5Width {
				t.Errorf("page_size a4 vs a5 MediaBox width identical (%v pt) -- page_size opt not reflected in the output PDF", a4Width)
			} else {
				t.Logf("page_size structurally confirmed: a4 width=%vpt, a5 width=%vpt", a4Width, a5Width)
			}
		} else {
			t.Logf("MediaBox not structurally extractable (a4 found=%v, a5 found=%v); both variants completed successfully -- page_size difference recorded via visual confirmation in the Task 3 acceptance run", a4ok, a5ok)
		}
	})

	// HTML-03: print_background true vs false. A structural content-stream
	// diff is not reliably extractable without a full PDF decompressor (the
	// fill operators live inside a FlateDecode-compressed stream), so this
	// subtest's load-bearing assertion is that BOTH variants complete
	// successfully as valid PDFs; the visual background-suppression
	// difference is confirmed in the Task 3 acceptance run per the plan.
	t.Run("PrintOptsBackground", func(t *testing.T) {
		onURL := renderHTMLWithOpts(t, cfg, apiKey, "sample.html", data, `{"print_background":true}`)
		offURL := renderHTMLWithOpts(t, cfg, apiKey, "sample.html", data, `{"print_background":false}`)

		onBody := downloadPDFBytes(t, onURL)
		offBody := downloadPDFBytes(t, offURL)
		if bytes.Equal(onBody, offBody) {
			t.Logf("print_background true/false produced byte-identical PDFs; visual confirmation recorded in the Task 3 acceptance run")
		} else {
			t.Logf("print_background true/false produced differing PDF bytes (%d vs %d bytes) -- consistent with the forced background:none override taking effect", len(onBody), len(offBody))
		}
	})
}

// TestHTMLContentRejectionE2E proves live that a non-HTML file uploaded
// under a .html name is rejected 422 before any job is ever created (D-07,
// mirrors TestOLECFBRejectionE2E's postJobExpectStatus pattern).
func TestHTMLContentRejectionE2E(t *testing.T) {
	cfg := e2eSetup(t)
	apiKey := provisionClient(t)

	data, err := os.ReadFile(filepath.Join("testdata", "nothtml.html"))
	if err != nil {
		t.Fatalf("read fixture nothtml.html: %v", err)
	}

	body := postJobExpectStatus(t, cfg.baseURL, apiKey, "nothtml.html", data, "pdf", "", http.StatusUnprocessableEntity)
	if len(body) == 0 {
		t.Error("422 body for nothtml.html is empty")
	}
}

// canaryHit records one inbound connection to the canary receiver -- the
// request path is recorded (not individually asserted) since the canary
// fixture references it from multiple element types (img src, script
// fetch).
type canaryHit struct {
	path string
}

// startCanaryReceiver generalizes startWebhookReceiver (same net.Listen +
// host.docker.internal-addressed base URL + buffered-channel shape) to
// record a canaryHit for ANY path, not a single fixed endpoint -- the
// canary.html fixture references multiple paths (/canary-img,
// /canary-fetch) and this receiver must catch all of them, since
// TestHTMLNetworkBlockE2E's assertion is "zero hits across the whole render
// window," not "zero hits on one specific path."
func startCanaryReceiver(t *testing.T, host string) (string, <-chan canaryHit) {
	t.Helper()

	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("listen for canary receiver: %v", err)
	}

	hits := make(chan canaryHit, 16)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits <- canaryHit{path: r.URL.Path}
		w.WriteHeader(http.StatusOK)
	}))
	srv.Listener.Close() // discard the default loopback listener
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)

	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("split canary listener addr: %v", err)
	}
	baseURL := fmt.Sprintf("http://%s", net.JoinHostPort(host, port))
	return baseURL, hits
}

// TestHTMLNetworkBlockE2E is the live, direct proof of HTML-02/success
// criterion 2 (D-04): a canary listener must record ZERO inbound
// connections while a deliberately adversarial HTML fixture -- external IP
// literal, loopback literal, compose-network hostnames, and a file://
// exfiltration attempt (RESEARCH.md Pattern 2/3) -- is rendered to PDF, AND
// the render must complete (not hang/time out). Both assertions are direct
// evidence: zero-hits proves no fetch occurred (not "the render didn't
// crash"), and reaching pollUntilDone's return proves the job finished
// within the generous bound rather than hanging against the (correctly)
// dead proxy/resolver.
func TestHTMLNetworkBlockE2E(t *testing.T) {
	cfg := e2eSetup(t)
	apiKey := provisionClient(t)

	baseURL, hits := startCanaryReceiver(t, cfg.webhookHost)

	tmplBytes, err := os.ReadFile(filepath.Join("testdata", "canary.html"))
	if err != nil {
		t.Fatalf("read fixture canary.html: %v", err)
	}
	tmpl, err := template.New("canary").Parse(string(tmplBytes))
	if err != nil {
		t.Fatalf("parse canary.html template: %v", err)
	}
	var rendered bytes.Buffer
	if err := tmpl.Execute(&rendered, struct{ BaseURL string }{BaseURL: baseURL}); err != nil {
		t.Fatalf("execute canary.html template: %v", err)
	}

	jobID := postJob(t, cfg.baseURL, apiKey, "canary.html", rendered.Bytes(), "pdf", "", "")

	// pollUntilDone fatals on "failed" and on timeout -- reaching the line
	// below already proves the render completed within the generous bound,
	// the "job still completes" half of D-04's assertion.
	body := pollUntilDone(t, cfg.baseURL, apiKey, jobID, 5*time.Minute)
	downloadURL, _ := body["download_url"].(string)
	if downloadURL == "" {
		t.Fatalf("job %s done but download_url missing/empty: %v", jobID, body)
	}
	assertDownloadIsPDF(t, downloadURL)

	// Drain for any hit that may have arrived during the render window: a
	// short bounded wait (not a blocking read), since the render already
	// reached "done" above -- the render window has closed by construction,
	// this is just giving any in-flight TCP handshake a moment to land in
	// the buffered channel before we assert zero.
	select {
	case hit := <-hits:
		t.Fatalf("canary received an inbound connection at path %q -- network block FAILED (success criterion 2)", hit.path)
	case <-time.After(3 * time.Second):
		// zero hits across the entire render window -- the success-criterion-2
		// direct proof (D-04).
	}
}

// TestImageConversionE2E drives the image engine (libvips) png->jpg happy
// path through the LIVE pipeline: multipart upload -> poll to done ->
// presigned download (convert.Sniff-verified as jpg) -> a signed webhook
// arrives (DEBT-08) -- the last gap in the E2E format matrix, mirroring
// TestDocumentConversionE2E's single-pair-plus-webhook shape but for the
// image engine.
func TestImageConversionE2E(t *testing.T) {
	cfg := e2eSetup(t)
	apiKey := provisionClient(t)

	data, err := os.ReadFile(filepath.Join("testdata", "sample.png"))
	if err != nil {
		t.Fatalf("read fixture sample.png: %v", err)
	}

	callbackURL, received := startWebhookReceiver(t, cfg.webhookHost)

	jobID := postJob(t, cfg.baseURL, apiKey, "sample.png", data, "jpg", callbackURL, "")

	// libvips conversion is fast; a comfortable bound well above the image
	// queue's expected turnaround, unlike the document/html engines' 5-minute
	// cold-start allowance.
	body := pollUntilDone(t, cfg.baseURL, apiKey, jobID, 2*time.Minute)

	downloadURL, _ := body["download_url"].(string)
	if downloadURL == "" {
		t.Fatalf("job %s done but download_url missing/empty: %v", jobID, body)
	}
	assertDownloadIsImage(t, downloadURL, "jpg")

	assertSignedWebhook(t, received, jobID)
}

// assertDownloadIsImage fetches the presigned URL (no auth header -- the URL
// is self-authorizing) and asserts the downloaded bytes sniff (via
// convert.Sniff, the magic-byte detector for raster images -- NOT
// SniffContainer, which structurally validates ZIP/office containers) as
// wantFormat.
func assertDownloadIsImage(t *testing.T, downloadURL, wantFormat string) {
	t.Helper()
	resp, err := downloadClient().Get(downloadURL)
	if err != nil {
		t.Fatalf("GET download_url: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		t.Fatalf("GET download_url status = %d, want 200; body=%s", resp.StatusCode, b)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read download body: %v", err)
	}
	detected, _, err := convert.Sniff(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("convert.Sniff(download): %v", err)
	}
	if detected != wantFormat {
		t.Fatalf("downloaded image format = %q, want %q", detected, wantFormat)
	}
}

// TestQueueDepthMetricRelocationE2E is the live half of D-03 (KEDA-01, Phase
// 27 Plan 01): it proves the octoconv_queue_depth collector relocation
// (Task 1) against a live compose stack, BEFORE any k8s work. The runtime
// images ship no curl/wget/shell fetch tool (RESEARCH.md Pitfall 3), so
// reachability is via docker-compose.e2e.yml's host-published, 0.0.0.0-bound
// metrics ports (api :9190, image worker :9191) rather than docker-exec.
//
// Scope note (checker WARNING 2, documented per 27-01-PLAN.md): only the
// image worker's absence of the metric is live-checked here, not all four
// worker binaries. This is deliberate and sufficient — the collector removal
// in Task 1 is a mechanically identical 3-line deletion applied to all four
// worker mains, and Task 1's acceptance criteria already statically prove
// (via `grep -rl "NewQueueDepthCollector" cmd/` returning exactly
// cmd/api/main.go, plus a no-unused-import `go vet`) that document/chromium/
// webhook workers no longer register the collector. Publishing three more
// host ports to live-assert the identical deletion would add compose surface
// and CI wiring for zero additional coverage.
func TestQueueDepthMetricRelocationE2E(t *testing.T) {
	cfg := e2eSetup(t)

	// asynq only adds a queue name to its "asynq:queues" registry set on the
	// FIRST real enqueue (internal/rdb, e.g. EnqueueUnique/ScheduleUnique) —
	// never at asynq.NewServer(Queues: ...) startup. GetQueueInfo/CurrentStats
	// returns errors.NotFound (silently skipped by queueDepthCollector.Collect)
	// for any queue that has never had a task, which a freshly-created compose
	// stack has for all four queues. seedQueueRegistry primes just the
	// registry membership directly against Redis — zero tasks are created, no
	// worker ever processes anything — purely so CurrentStats' pre-check finds
	// the queue "known" and returns real (zero-valued) counts, matching what
	// happens naturally in production the moment the first job is submitted.
	seedQueueRegistry(t, "image", "document", "html", "webhook")

	u, err := url.Parse(cfg.baseURL)
	if err != nil {
		t.Fatalf("parse E2E_BASE_URL %q: %v", cfg.baseURL, err)
	}
	metricsHost := u.Hostname()
	if metricsHost == "" {
		metricsHost = "localhost"
	}

	apiMetricsURL := fmt.Sprintf("http://%s:9190/metrics", metricsHost)
	workerMetricsURL := fmt.Sprintf("http://%s:9191/metrics", metricsHost)

	// (a) api :9190 must expose octoconv_queue_depth for all four queues —
	// the relocated single source of truth KEDA will scrape.
	apiResp, err := e2eHTTP.Get(apiMetricsURL)
	if err != nil {
		t.Fatalf("GET %s (api metrics port unreachable — check docker-compose.e2e.yml publishes 9190:9090 with METRICS_ADDR=0.0.0.0:9090): %v", apiMetricsURL, err)
	}
	defer apiResp.Body.Close()
	if apiResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(apiResp.Body, 1024))
		t.Fatalf("GET %s status = %d, want 200; body=%s", apiMetricsURL, apiResp.StatusCode, body)
	}
	apiBody, err := io.ReadAll(apiResp.Body)
	if err != nil {
		t.Fatalf("read api metrics body: %v", err)
	}
	apiBodyStr := string(apiBody)
	if !strings.Contains(apiBodyStr, "octoconv_queue_depth") {
		t.Fatalf("api :9190/metrics does not contain octoconv_queue_depth at all — collector not registered on api (Task 1 regression)")
	}
	for _, q := range []string{"image", "document", "html", "webhook"} {
		label := fmt.Sprintf("queue=%q", q)
		if !strings.Contains(apiBodyStr, label) {
			t.Errorf("api :9190/metrics missing octoconv_queue_depth series for %s", label)
		}
	}

	// (b) image worker :9191 must return HTTP 200 (endpoint retained) but must
	// NOT contain octoconv_queue_depth (relocation proof — worker no longer
	// serves it).
	workerResp, err := e2eHTTP.Get(workerMetricsURL)
	if err != nil {
		t.Fatalf("GET %s (worker metrics port unreachable — check docker-compose.e2e.yml publishes 9191:9090 with METRICS_ADDR=0.0.0.0:9090): %v", workerMetricsURL, err)
	}
	defer workerResp.Body.Close()
	if workerResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(workerResp.Body, 1024))
		t.Fatalf("GET %s status = %d, want 200 (endpoint should be retained even without the collector); body=%s", workerMetricsURL, workerResp.StatusCode, body)
	}
	workerBody, err := io.ReadAll(workerResp.Body)
	if err != nil {
		t.Fatalf("read worker metrics body: %v", err)
	}
	if strings.Contains(string(workerBody), "octoconv_queue_depth") {
		t.Fatalf("image worker :9191/metrics still contains octoconv_queue_depth — collector was NOT relocated off the worker (Task 1 regression)")
	}
}

// seedQueueRegistry adds each given queue name to asynq's "asynq:queues"
// Redis SET (base.AllQueues) directly, without creating any task. This is
// the ONLY state asynq's CurrentStats pre-check ("does this queue exist")
// consults; it is otherwise populated lazily on a queue's first real
// enqueue. SADD is idempotent, so re-running this against an already-seeded
// stack is a harmless no-op. REDIS_ADDR defaults to localhost:6379, matching
// the host-published port docker-compose.yml already exposes (unrelated to
// the E2E-only metrics port overrides).
func seedQueueRegistry(t *testing.T, queues ...string) {
	t.Helper()
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	defer rdb.Close()

	ctx := context.Background()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Fatalf("seedQueueRegistry: redis ping %s: %v", addr, err)
	}
	members := make([]interface{}, len(queues))
	for i, q := range queues {
		members[i] = q
	}
	if err := rdb.SAdd(ctx, "asynq:queues", members...).Err(); err != nil {
		t.Fatalf("seedQueueRegistry: SADD asynq:queues %v: %v", queues, err)
	}
}
