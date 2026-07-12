package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// TestByIP_NotEvadedByForwardedForSpoofing reproduces Gap 2 (RATE-03 /
// ROADMAP SC5 / 01-REVIEW.md CR-01) through the real router: 5 sequential
// requests share one real RemoteAddr (the default httptest peer) but each
// carries a distinct spoofed X-Forwarded-For header. Against a coarse ByIP
// limit of 2/min, the first two requests must pass the limiter (falling
// through to auth, where they get 401 since they carry no API key — that's
// expected and irrelevant to this test) and the remaining three must be
// rejected with 429 by ratelimit.ByIP. If all 5 pass, the coarse pre-auth
// flood guard is being evaded by header variation.
func TestByIP_NotEvadedByForwardedForSpoofing(t *testing.T) {
	srv := NewServer(&fakeRepo{}, &fakeStorage{}, &fakeQueue{}, defaultFakePresetRepo(), newFakeResolver(), healthyDeps(), Config{IPRateLimitRPM: 2, MaxUploadBytes: 1 << 20})
	h := srv.Routes()

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/v1/jobs/"+uuid.New().String(), nil)
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("10.9.8.%d", i+1))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if i < 2 {
			if rec.Code == http.StatusTooManyRequests {
				t.Fatalf("request %d: got 429, want it to pass ByIP (401 from auth is fine); spoofed X-Forwarded-For=10.9.8.%d", i+1, i+1)
			}
		} else {
			if rec.Code != http.StatusTooManyRequests {
				t.Fatalf("request %d: got status %d, want 429 (coarse IP limit evaded by spoofed X-Forwarded-For=10.9.8.%d)", i+1, rec.Code, i+1)
			}
		}
	}
}
