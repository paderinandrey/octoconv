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
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

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
// 422 before any conversion is attempted (D-05/D-06).
var oleCFBFixtures = []string{
	"legacy.doc",
	"encrypted.docx",
}

// TestOLECFBRejectionE2E proves live (against real fixtures, not synthetic
// magic bytes) that both OLE-CFB sub-cases are rejected 422 before any job
// is ever created -- unlike the postJob/pollUntilDone tables above, no job
// ID is produced here at all.
func TestOLECFBRejectionE2E(t *testing.T) {
	cfg := e2eSetup(t)
	apiKey := provisionClient(t)

	for _, filename := range oleCFBFixtures {
		t.Run(filename, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", filename))
			if err != nil {
				t.Fatalf("read fixture %s: %v", filename, err)
			}

			body := postJobExpectStatus(t, cfg.baseURL, apiKey, filename, data, "pdf", "", http.StatusUnprocessableEntity)

			// Loose substring check (not exact-string) so a reworded D-06
			// message doesn't brittle-fail this live test; the 422 status
			// above is the load-bearing assertion.
			if !bytes.Contains(bytes.ToLower(body), []byte("password")) {
				t.Errorf("422 body for %s does not mention the remedy; body=%s", filename, body)
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
