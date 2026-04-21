package http_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/chaustre/inquiryiq/internal/domain"
	transporthttp "github.com/chaustre/inquiryiq/internal/transport/http"
)

func signedHeader(secret, id, ts string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(id + "." + ts + "."))
	m.Write(body)
	return "v1," + base64.StdEncoding.EncodeToString(m.Sum(nil))
}

func TestVerifySignatureValid(t *testing.T) {
	t.Parallel()
	const secret = "whsec_test"
	id := "msg_01"
	now := time.Now().UTC()
	ts := strconv.FormatInt(now.Unix(), 10)
	body := []byte(`{"ok":true}`)
	sig := signedHeader(secret, id, ts, body)
	if err := transporthttp.VerifySignature(secret, id, ts, body, sig, time.Minute, now); err != nil {
		t.Fatalf("valid sig rejected: %v", err)
	}
}

func TestVerifySignatureWrongSecretRejected(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	ts := strconv.FormatInt(now.Unix(), 10)
	body := []byte(`{}`)
	sig := signedHeader("good", "id1", ts, body)
	err := transporthttp.VerifySignature("bad", "id1", ts, body, sig, time.Minute, now)
	if !errors.Is(err, domain.ErrWebhookSignatureInvalid) {
		t.Fatalf("want ErrWebhookSignatureInvalid, got %v", err)
	}
}

func TestVerifySignatureClockDriftRejected(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	old := now.Add(-10 * time.Minute)
	ts := strconv.FormatInt(old.Unix(), 10)
	body := []byte(`{}`)
	sig := signedHeader("s", "id1", ts, body)
	err := transporthttp.VerifySignature("s", "id1", ts, body, sig, 5*time.Minute, now)
	if !errors.Is(err, domain.ErrWebhookClockDrift) {
		t.Fatalf("want ErrWebhookClockDrift, got %v", err)
	}
}

func TestVerifySignatureMultiVersionAcceptsEitherMatch(t *testing.T) {
	t.Parallel()
	const secret = "whsec_test"
	id := "msg_02"
	now := time.Now().UTC()
	ts := strconv.FormatInt(now.Unix(), 10)
	body := []byte(`{"ok":1}`)
	good := signedHeader(secret, id, ts, body)
	bad := "v1," + base64.StdEncoding.EncodeToString([]byte("not-a-real-signature-payload"))
	// good listed second; parser must still accept it.
	multi := fmt.Sprintf("%s %s", bad, good)
	if err := transporthttp.VerifySignature(secret, id, ts, body, multi, time.Minute, now); err != nil {
		t.Fatalf("multi-version sig rejected: %v", err)
	}
}

func TestVerifySignatureMalformedTimestampTreatedAsDrift(t *testing.T) {
	t.Parallel()
	err := transporthttp.VerifySignature("s", "id1", "not-a-number", []byte(`{}`), "v1,aaaa", time.Minute, time.Now())
	if !errors.Is(err, domain.ErrWebhookClockDrift) {
		t.Fatalf("want ErrWebhookClockDrift, got %v", err)
	}
}

func TestVerifySignatureIgnoresUnknownVersion(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	ts := strconv.FormatInt(now.Unix(), 10)
	body := []byte(`{}`)
	sig := "v99,aaaa " // unknown version only
	err := transporthttp.VerifySignature("s", "id", ts, body, sig, time.Minute, now)
	if !errors.Is(err, domain.ErrWebhookSignatureInvalid) {
		t.Fatalf("want ErrWebhookSignatureInvalid, got %v", err)
	}
}
