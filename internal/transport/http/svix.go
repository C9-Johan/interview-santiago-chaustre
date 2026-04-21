package http

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/chaustre/inquiryiq/internal/domain"
)

// VerifySignature checks a Svix-style HMAC-SHA256 signature against the raw
// request body. The signed payload is `id.ts.body` (literal dots) and sig is
// the header value, which may contain multiple space-separated versions
// (`v1,<base64> v1,<base64>`). Any v1 match validates. Timestamp drift beyond
// maxDrift returns ErrWebhookClockDrift; a signature mismatch returns
// ErrWebhookSignatureInvalid. A malformed timestamp is treated as drift.
func VerifySignature(secret, id, ts string, body []byte, sig string, maxDrift time.Duration, now time.Time) error {
	if err := checkDrift(ts, maxDrift, now); err != nil {
		return err
	}
	if err := checkSignature(secret, id, ts, body, sig); err != nil {
		return err
	}
	return nil
}

func checkDrift(ts string, maxDrift time.Duration, now time.Time) error {
	secs, err := strconv.ParseInt(strings.TrimSpace(ts), 10, 64)
	if err != nil {
		return fmt.Errorf("%w: unparseable timestamp", domain.ErrWebhookClockDrift)
	}
	delivered := time.Unix(secs, 0)
	diff := now.Sub(delivered)
	if diff < 0 {
		diff = -diff
	}
	if diff > maxDrift {
		return fmt.Errorf("%w: drift %s > max %s", domain.ErrWebhookClockDrift, diff, maxDrift)
	}
	return nil
}

func checkSignature(secret, id, ts string, body []byte, sig string) error {
	expected := computeSignature(secret, id, ts, body)
	for _, candidate := range parseSignatures(sig) {
		if hmac.Equal(candidate, expected) {
			return nil
		}
	}
	return domain.ErrWebhookSignatureInvalid
}

func computeSignature(secret, id, ts string, body []byte) []byte {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(id))
	mac.Write([]byte{'.'})
	mac.Write([]byte(ts))
	mac.Write([]byte{'.'})
	mac.Write(body)
	return mac.Sum(nil)
}

func parseSignatures(raw string) [][]byte {
	parts := strings.Fields(raw)
	out := make([][]byte, 0, len(parts))
	for _, p := range parts {
		idx := strings.IndexByte(p, ',')
		if idx < 0 || p[:idx] != "v1" {
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(p[idx+1:])
		if err != nil {
			continue
		}
		out = append(out, decoded)
	}
	return out
}
