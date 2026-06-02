package agent

import (
	"context"
	"errors"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/message"
)

// sleepFunc waits for d or until ctx is cancelled, returning ctx.Err() if the
// context is cancelled first. Tests inject a no-op (optionally recording)
// implementation so retries exercise the backoff schedule without real sleeping.
type sleepFunc func(ctx context.Context, d time.Duration) error

// contextSleep is the production sleepFunc: it blocks for d but unblocks early
// when ctx is cancelled (for example by Interrupt mid-backoff), surfacing the
// cancellation as an error so the loop stops promptly.
func contextSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// retryableProviderError reports whether a provider error is a transient
// failure worth retrying. The agent loop only ever sees wrapped llm sentinels,
// so it gates on those: a rate-limit or a transient server error is retryable,
// while every other error (auth, context limit, unsupported feature, an
// arbitrary terminal error) is permanent and returned immediately.
//
// llm.ShouldRetry is the HTTP-status gate used inside the provider clients; at
// the agent layer there is no status code, so sentinel matching via errors.Is
// is the correct discriminator.
func retryableProviderError(err error) bool {
	return errors.Is(err, llm.ErrRateLimit) || errors.Is(err, llm.ErrServer)
}

// callProviderWithRetry invokes callProvider and retries transient provider
// failures using the configured capped-exponential backoff. callProvider
// persists nothing, so re-invoking it is safe and idempotent.
//
// A non-retryable error returns immediately. A retryable error is retried up to
// Backoff.Attempts() total attempts; once the cap is reached the last error is
// returned rather than retrying forever. Each backoff wait honours ctx, so an
// interrupt mid-backoff returns the context error promptly.
func (l *Loop) callProviderWithRetry(ctx context.Context, history []message.Message) (message.Message, []pendingToolCall, *llm.Usage, error) {
	attempts := l.backoff.Attempts()
	var (
		msg   message.Message
		calls []pendingToolCall
		usage *llm.Usage
		err   error
	)
	for attempt := 1; attempt <= attempts; attempt++ {
		msg, calls, usage, err = l.callProvider(ctx, history)
		if err == nil {
			return msg, calls, usage, nil
		}
		// Stop on a permanent error, or once the final attempt has failed so the
		// loop never retries without bound.
		if !retryableProviderError(err) || attempt == attempts {
			return msg, calls, usage, err
		}
		if serr := l.sleep(ctx, l.backoff.Delay(attempt)); serr != nil {
			return message.Message{}, nil, nil, serr
		}
	}
	return msg, calls, usage, err
}
