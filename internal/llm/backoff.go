package llm

import (
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Backoff computes retry delays for transient provider failures using a
// capped exponential schedule with optional full jitter.
//
// The schedule follows the documented contract: exponential growth from a 1s
// base, doubling each attempt, capped at 32s, with at most 5 attempts for
// rate-limit retries. Jitter is enabled by default and can be disabled for
// deterministic tests.
type Backoff struct {
	// Base is the delay for the first attempt before doubling. Zero selects
	// the default of 1s.
	Base time.Duration
	// Cap is the maximum delay for any attempt. Zero selects the default of
	// 32s.
	Cap time.Duration
	// MaxAttempts is the maximum number of attempts before giving up. Zero
	// selects the default of 5.
	MaxAttempts int
	// NoJitter disables full jitter so Delay returns the exact capped
	// exponential value. Tests set this for determinism.
	NoJitter bool

	// now returns the current time. Tests inject a fixed clock; production
	// callers leave it nil to use time.Now.
	now func() time.Time
	// randFloat returns a uniform value in [0,1) used for full jitter. Tests
	// inject a deterministic source; production leaves it nil to use
	// math/rand/v2.
	randFloat func() float64
}

const (
	defaultBackoffBase = 1 * time.Second
	defaultBackoffCap  = 32 * time.Second
	defaultMaxAttempts = 5
)

func (b Backoff) base() time.Duration {
	if b.Base <= 0 {
		return defaultBackoffBase
	}
	return b.Base
}

func (b Backoff) cap() time.Duration {
	if b.Cap <= 0 {
		return defaultBackoffCap
	}
	return b.Cap
}

// Attempts returns the configured maximum number of attempts.
func (b Backoff) Attempts() int {
	if b.MaxAttempts <= 0 {
		return defaultMaxAttempts
	}
	return b.MaxAttempts
}

// Delay returns the backoff delay before the given 1-indexed attempt.
//
// The unjittered value is base*2^(attempt-1) capped at Cap, so with the
// defaults attempt 1 waits 1s, attempt 2 waits 2s, then 4s, 8s, 16s, and
// every later attempt is held at the 32s cap. Attempts below 1 are treated as
// attempt 1. When jitter is enabled the returned value is uniform in
// [0, cappedDelay]; when NoJitter is set it equals the capped delay exactly.
func (b Backoff) Delay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}

	d := b.base()
	c := b.cap()
	for i := 1; i < attempt; i++ {
		d *= 2
		if d >= c {
			d = c
			break
		}
	}
	if d > c {
		d = c
	}

	if b.NoJitter {
		return d
	}
	return time.Duration(b.randomFloat() * float64(d))
}

func (b Backoff) randomFloat() float64 {
	if b.randFloat != nil {
		return b.randFloat()
	}
	return rand.Float64()
}

func (b Backoff) clock() time.Time {
	if b.now != nil {
		return b.now()
	}
	return time.Now()
}

// RetryAfter returns the delay requested by a Retry-After header, honoring the
// configured cap. The boolean reports whether the header carried a usable
// value. HTTP-date forms are resolved against the injectable clock.
func (b Backoff) RetryAfter(header string) (time.Duration, bool) {
	d, ok := parseRetryAfterFrom(b.clock(), header)
	if !ok {
		return 0, false
	}
	if c := b.cap(); d > c {
		d = c
	}
	return d, true
}

// parseRetryAfter parses a Retry-After header value, supporting both the
// delta-seconds form ("120") and the HTTP-date form
// ("Wed, 21 Oct 2015 07:28:00 GMT"). It reports whether parsing succeeded.
// HTTP dates are resolved against the current wall clock.
func parseRetryAfter(header string) (time.Duration, bool) {
	return parseRetryAfterFrom(time.Now(), header)
}

// parseRetryAfterFrom is the clock-injectable core of parseRetryAfter. HTTP
// dates earlier than now clamp to a zero delay rather than going negative.
func parseRetryAfterFrom(now time.Time, header string) (time.Duration, bool) {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0, false
	}

	if secs, err := strconv.Atoi(header); err == nil {
		if secs < 0 {
			return 0, false
		}
		return time.Duration(secs) * time.Second, true
	}

	if t, err := http.ParseTime(header); err == nil {
		d := t.Sub(now)
		if d < 0 {
			d = 0
		}
		return d, true
	}

	return 0, false
}

// ShouldRetry reports whether a request that returned the given HTTP status
// code or transport error is worth retrying.
//
// A status code of 429 or any 5xx is retryable. Any other 4xx is terminal and
// not retried. A zero status code paired with a non-nil error denotes a
// transport-level failure (timeout, connection reset) and is retryable.
func ShouldRetry(statusCode int, err error) bool {
	if statusCode > 0 {
		if statusCode == http.StatusTooManyRequests {
			return true
		}
		return statusCode >= 500 && statusCode <= 599
	}
	return err != nil
}
