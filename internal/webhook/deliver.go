package webhook

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// Deliverer POSTs a signed webhook body to a client-supplied callback URL.
type Deliverer struct {
	hc *http.Client
}

// NewDeliverer builds a Deliverer bound to a 10s per-attempt HTTP timeout
// (D-08), independent of the task queue's own inter-attempt retry backoff
// (D-05).
//
// CheckRedirect refuses to follow any redirect: a callback_url that passed
// SSRF validation once (D-03) could otherwise 302 the delivery to a blocked
// address (e.g. 169.254.169.254) and bypass isBlockedIP entirely, since
// net/http follows redirects automatically by default. Returning
// http.ErrUseLastResponse makes the redirect response itself the result,
// which the 2xx-only success check below correctly treats as a failure.
func NewDeliverer() *Deliverer {
	return &Deliverer{hc: &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
}

// Deliver POSTs body to url with signature/timestamp headers, executing a
// single delivery attempt. Success is HTTP 2xx (D-07); any other status code,
// timeout, or network/transport error returns a plain, unwrapped error so the
// caller lets the queue's own retry policy apply (D-05). This package has no
// dependency on the task-queue library — it stays queue-agnostic.
func (d *Deliverer) Deliver(ctx context.Context, url string, body []byte, timestamp int64, signature string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("webhook delivery to %q: %w", url, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-OctoConv-Signature", signature)
	req.Header.Set("X-OctoConv-Timestamp", strconv.FormatInt(timestamp, 10))

	resp, err := d.hc.Do(req)
	if err != nil {
		return 0, fmt.Errorf("webhook delivery to %q: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp.StatusCode, nil
	}
	return resp.StatusCode, fmt.Errorf("webhook delivery to %q: status %d", url, resp.StatusCode)
}
