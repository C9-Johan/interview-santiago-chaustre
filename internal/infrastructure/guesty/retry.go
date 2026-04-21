package guesty

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"net/http"
	"strconv"
	"time"
)

// ErrRetriesExhausted is returned when all retry attempts have failed.
var ErrRetriesExhausted = errors.New("guesty: retries exhausted")

// shouldRetry returns the retry delay for resp at attempt number. A zero
// return means the caller must not retry. Honors Retry-After on 429 when the
// server provides an integer-seconds value; otherwise falls back to
// exponential backoff with jitter.
func shouldRetry(resp *http.Response, attempt int, base time.Duration) time.Duration {
	if resp == nil {
		return backoff(attempt, base)
	}
	switch resp.StatusCode {
	case http.StatusTooManyRequests:
		if d, ok := retryAfter(resp); ok {
			return d
		}
		return backoff(attempt, base)
	case http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return backoff(attempt, base)
	}
	return 0
}

func retryAfter(resp *http.Response) (time.Duration, bool) {
	ra := resp.Header.Get("Retry-After")
	if ra == "" {
		return 0, false
	}
	s, err := strconv.Atoi(ra)
	if err != nil {
		return 0, false
	}
	if s <= 0 {
		return 0, false
	}
	return time.Duration(s) * time.Second, true
}

func backoff(attempt int, base time.Duration) time.Duration {
	d := base * (1 << attempt)
	return time.Duration(float64(d) * (0.8 + 0.4*randFloat()))
}

// randFloat returns a uniform value in [0,1) using crypto/rand so gosec stays
// quiet and jitter is unpredictable to an external observer.
func randFloat() float64 {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// On entropy failure return the midpoint; jitter is advisory
		// and correctness does not rely on the exact multiplier.
		return 0.5
	}
	// Top 53 bits → IEEE-754 double mantissa, divided by 2^53 → [0,1).
	n := binary.BigEndian.Uint64(b[:]) >> 11
	return float64(n) / float64(uint64(1)<<53)
}
