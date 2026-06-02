package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/message"
)

// stubSleep replaces the package retry sleep with a recorder so tests run
// offline without real delays, restoring the original on cleanup.
func stubSleep(t *testing.T) *[]time.Duration {
	t.Helper()
	prev := sleepFn
	var slept []time.Duration
	sleepFn = func(d time.Duration) { slept = append(slept, d) }
	t.Cleanup(func() { sleepFn = prev })
	return &slept
}

func TestRetriesOn429ThenSucceeds(t *testing.T) {
	slept := stubSleep(t)

	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"error":{"type":"rate_limit_error"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":2}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	reg, err := NewRegistry(testConfig("deepseek", config.ProviderOpenAICompatible, server.URL+"/v1"))
	require.NoError(t, err)
	provider, err := reg.Get("deepseek")
	require.NoError(t, err)

	events, err := provider.Stream(context.Background(), Request{
		Model:    "test-model",
		Messages: []message.Message{{Role: message.RoleUser, Content: []message.ContentBlock{message.TextBlock{Text: "hi"}}}},
	})
	require.NoError(t, err)

	got := collectEvents(events)
	// The retried request ultimately succeeded and streamed real content.
	require.Contains(t, got, DeltaTextEvent{Text: "ok"})
	require.Contains(t, got, EndEvent{Usage: Usage{InputTokens: 5, OutputTokens: 2}})

	// The server was hit exactly twice: the 429 then the 200.
	require.Equal(t, int32(2), atomic.LoadInt32(&calls))

	// Exactly one sleep happened, honoring the Retry-After of 7s rather than
	// the exponential schedule.
	require.Len(t, *slept, 1)
	require.Equal(t, 7*time.Second, (*slept)[0])
}

func TestRetriesUseExponentialDelayWithoutRetryAfter(t *testing.T) {
	slept := stubSleep(t)

	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	reg, err := NewRegistry(testConfig("deepseek", config.ProviderOpenAICompatible, server.URL+"/v1"))
	require.NoError(t, err)
	provider, err := reg.Get("deepseek")
	require.NoError(t, err)

	events, err := provider.Stream(context.Background(), Request{Model: "test-model"})
	require.NoError(t, err)
	_ = collectEvents(events)

	// Two 503s then a 200 => three calls, two backoff sleeps.
	require.Equal(t, int32(3), atomic.LoadInt32(&calls))
	require.Equal(t, []time.Duration{1 * time.Second, 2 * time.Second}, *slept)
}

func TestDoesNotRetry400(t *testing.T) {
	slept := stubSleep(t)

	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"message":"bad input"}}`)
	}))
	defer server.Close()

	reg, err := NewRegistry(testConfig("deepseek", config.ProviderOpenAICompatible, server.URL+"/v1"))
	require.NoError(t, err)
	provider, err := reg.Get("deepseek")
	require.NoError(t, err)

	_, err = provider.Stream(context.Background(), Request{Model: "test-model"})
	require.Error(t, err)

	// A 400 is terminal: hit once, never slept.
	require.Equal(t, int32(1), atomic.LoadInt32(&calls))
	require.Empty(t, *slept)
}

func TestRetriesExhaustReturnsClassifiedError(t *testing.T) {
	stubSleep(t)

	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{}`)
	}))
	defer server.Close()

	reg, err := NewRegistry(testConfig("deepseek", config.ProviderOpenAICompatible, server.URL+"/v1"))
	require.NoError(t, err)
	provider, err := reg.Get("deepseek")
	require.NoError(t, err)

	_, err = provider.Stream(context.Background(), Request{Model: "test-model"})
	// After exhausting attempts the final 429 surfaces as ErrRateLimit.
	require.ErrorIs(t, err, ErrRateLimit)

	// The default backoff allows 5 attempts.
	require.Equal(t, int32(retryBackoff.Attempts()), atomic.LoadInt32(&calls))
}
