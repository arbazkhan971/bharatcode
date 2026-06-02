package llm

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestBackoffDelaySequenceNoJitter(t *testing.T) {
	b := Backoff{NoJitter: true}

	want := []time.Duration{
		1 * time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		16 * time.Second,
	}
	for i, w := range want {
		attempt := i + 1
		if got := b.Delay(attempt); got != w {
			t.Errorf("Delay(%d) = %v, want %v", attempt, got, w)
		}
	}
}

func TestBackoffDelayCapsAt32s(t *testing.T) {
	b := Backoff{NoJitter: true}

	// Attempt 6 would be 32s exactly; attempt 7 and beyond stay capped.
	for _, attempt := range []int{6, 7, 10, 100} {
		if got := b.Delay(attempt); got != 32*time.Second {
			t.Errorf("Delay(%d) = %v, want capped 32s", attempt, got)
		}
	}
}

func TestBackoffDelayAttemptBelowOne(t *testing.T) {
	b := Backoff{NoJitter: true}
	if got := b.Delay(0); got != 1*time.Second {
		t.Errorf("Delay(0) = %v, want 1s", got)
	}
}

func TestBackoffDefaultMaxAttempts(t *testing.T) {
	if got := (Backoff{}).Attempts(); got != 5 {
		t.Errorf("Attempts() = %d, want 5", got)
	}
}

func TestBackoffFullJitterBounds(t *testing.T) {
	// randFloat returns 0.5, so the jittered delay is exactly half the capped
	// exponential value: a real, deterministic assertion of the jitter math.
	b := Backoff{randFloat: func() float64 { return 0.5 }}

	if got := b.Delay(3); got != 2*time.Second { // half of 4s
		t.Errorf("jittered Delay(3) = %v, want 2s", got)
	}

	// Endpoints of the [0, cap] full-jitter range.
	lo := Backoff{randFloat: func() float64 { return 0 }}
	if got := lo.Delay(2); got != 0 {
		t.Errorf("jittered Delay(2) with rand=0 = %v, want 0", got)
	}
	hi := Backoff{randFloat: func() float64 { return 0.999999999 }}
	if got := hi.Delay(2); got <= 0 || got > 2*time.Second {
		t.Errorf("jittered Delay(2) with rand~=1 = %v, want in (0, 2s]", got)
	}
}

func TestParseRetryAfterDeltaSeconds(t *testing.T) {
	got, ok := parseRetryAfter("3")
	if !ok {
		t.Fatalf("parseRetryAfter(%q) ok = false, want true", "3")
	}
	if got != 3*time.Second {
		t.Errorf("parseRetryAfter(%q) = %v, want 3s", "3", got)
	}
}

func TestParseRetryAfterHTTPDate(t *testing.T) {
	now := time.Date(2015, time.October, 21, 7, 28, 0, 0, time.UTC)
	// 120 seconds after now.
	header := "Wed, 21 Oct 2015 07:30:00 GMT"

	got, ok := parseRetryAfterFrom(now, header)
	if !ok {
		t.Fatalf("parseRetryAfterFrom(%q) ok = false, want true", header)
	}
	if got != 2*time.Minute {
		t.Errorf("parseRetryAfterFrom(%q) = %v, want 2m", header, got)
	}
}

func TestParseRetryAfterPublicParsesHTTPDate(t *testing.T) {
	// Exercise the literal public parseRetryAfter against a far-future GMT
	// date so the date branch is shown working through the wrapper, not only
	// the clock-injected core.
	header := time.Now().Add(1 * time.Hour).UTC().Format(http.TimeFormat)

	got, ok := parseRetryAfter(header)
	if !ok {
		t.Fatalf("parseRetryAfter(%q) ok = false, want true", header)
	}
	if got <= 0 {
		t.Errorf("parseRetryAfter(%q) = %v, want positive duration", header, got)
	}
}

func TestParseRetryAfterPastDateClampsToZero(t *testing.T) {
	now := time.Date(2015, time.October, 21, 7, 28, 0, 0, time.UTC)
	header := "Wed, 21 Oct 2015 07:00:00 GMT" // in the past

	got, ok := parseRetryAfterFrom(now, header)
	if !ok {
		t.Fatalf("parseRetryAfterFrom past date ok = false, want true")
	}
	if got != 0 {
		t.Errorf("parseRetryAfterFrom past date = %v, want 0", got)
	}
}

func TestParseRetryAfterInvalid(t *testing.T) {
	for _, header := range []string{"", "   ", "not-a-date", "-5"} {
		if got, ok := parseRetryAfter(header); ok {
			t.Errorf("parseRetryAfter(%q) = (%v, true), want ok=false", header, got)
		}
	}
}

func TestBackoffRetryAfterHonorsCap(t *testing.T) {
	b := Backoff{Cap: 32 * time.Second}
	got, ok := b.RetryAfter("600") // 10m, well above the cap
	if !ok {
		t.Fatalf("RetryAfter ok = false, want true")
	}
	if got != 32*time.Second {
		t.Errorf("RetryAfter(600s) = %v, want capped 32s", got)
	}
}

func TestShouldRetry(t *testing.T) {
	tests := []struct {
		name   string
		status int
		err    error
		want   bool
	}{
		{name: "rate limit 429", status: http.StatusTooManyRequests, want: true},
		{name: "server 500", status: http.StatusInternalServerError, want: true},
		{name: "server 503", status: http.StatusServiceUnavailable, want: true},
		{name: "bad request 400", status: http.StatusBadRequest, want: false},
		{name: "unauthorized 401", status: http.StatusUnauthorized, want: false},
		{name: "not found 404", status: http.StatusNotFound, want: false},
		{name: "ok 200", status: http.StatusOK, want: false},
		{name: "transport error", status: 0, err: errors.New("connection reset"), want: true},
		{name: "no status no error", status: 0, err: nil, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShouldRetry(tt.status, tt.err); got != tt.want {
				t.Errorf("ShouldRetry(%d, %v) = %v, want %v", tt.status, tt.err, got, tt.want)
			}
		})
	}
}
