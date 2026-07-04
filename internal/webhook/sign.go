package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
)

// SignPayload returns a deterministic HMAC-SHA256 digest (64-char lowercase
// hex) of the message "<timestamp>.<body>". HMAC-SHA256 gives a receiver
// integrity/authenticity assurance that the payload originated from this
// service and was not altered in transit (D-01, WEBHOOK-02); binding the
// timestamp into the signed message (rather than signing the body alone)
// lets receivers reject stale/replayed deliveries by rejecting requests whose
// timestamp is too old, even though the body itself would otherwise still
// verify. secret is a parameter, never read from the environment inside this
// package — mirrors HashKey's salt parameter (internal/auth/hash.go).
func SignPayload(secret []byte, timestamp int64, body []byte) string {
	msg := strconv.FormatInt(timestamp, 10) + "." + string(body)
	h := hmac.New(sha256.New, secret)
	h.Write([]byte(msg))
	return hex.EncodeToString(h.Sum(nil))
}
