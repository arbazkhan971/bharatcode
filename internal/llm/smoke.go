package llm

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/message"
)

// DefaultSmokeTimeout bounds a Smoke probe so a stalled or unreachable provider
// cannot hang the caller. It is intentionally short: the probe only needs the
// provider to accept the request and begin answering, not to finish a long
// generation.
const DefaultSmokeTimeout = 15 * time.Second

// smokePrompt is the trivial question the probe asks. It is deliberately tiny so
// the request costs almost nothing and any cooperative model answers quickly.
const smokePrompt = "Reply with the single word: ok"

// SmokeResult reports the outcome of a Smoke probe.
type SmokeResult struct {
	// OK is true when the provider accepted the request and produced at least
	// one token of output before the stream ended without error.
	OK bool
	// Reply is the assistant text the probe collected, trimmed to a short
	// preview. It is empty when the probe failed before any text arrived.
	Reply string
	// Latency is how long the probe took from request to first terminal event.
	Latency time.Duration
}

// Smoke makes one minimal request against provider using model and reports
// whether the configured model can actually answer right now. It drains a single
// Stream: success requires the stream to end normally after emitting some text,
// which proves end-to-end that auth, routing, and the model id are all good.
//
// The returned error is one of the package's sentinel errors (ErrAuth,
// ErrModelNotFound, ErrRateLimit, ErrServer, ...) wrapped with context, so
// callers can give an actionable hint. A nil error means the probe answered.
//
// timeout caps the whole probe; pass DefaultSmokeTimeout for the standard bound.
// Smoke never panics on a nil provider — that is reported as an error.
func Smoke(ctx context.Context, p Provider, model string, timeout time.Duration) (SmokeResult, error) {
	if p == nil {
		return SmokeResult{}, errors.New("smoke check: provider is nil")
	}
	if timeout <= 0 {
		timeout = DefaultSmokeTimeout
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	req := Request{
		Model: model,
		Messages: []message.Message{{
			Role:    message.RoleUser,
			Content: []message.ContentBlock{message.TextBlock{Text: smokePrompt}},
		}},
		// A handful of tokens is plenty to confirm the model answers; capping
		// MaxTokens keeps the probe cheap even on a chatty model.
		MaxTokens:   16,
		Temperature: 0,
	}

	events, err := p.Stream(ctx, req)
	if err != nil {
		return SmokeResult{}, fmt.Errorf("smoke check: %w", err)
	}

	var reply string
	for ev := range events {
		switch e := ev.(type) {
		case DeltaTextEvent:
			reply += e.Text
		case ErrorEvent:
			// Drain the rest of the channel so the provider goroutine is not left
			// blocked on a send, then surface the failure to the caller.
			drainEvents(events)
			return SmokeResult{Latency: time.Since(start)}, fmt.Errorf("smoke check: %w", e.Err)
		case EndEvent:
			// EndEvent is terminal; the provider closes the channel after it.
		}
	}

	if ctxErr := ctx.Err(); ctxErr != nil && reply == "" {
		// The context expired before any text arrived: treat a timed-out probe as
		// a server-side stall so the caller's hint points at reachability/latency.
		return SmokeResult{Latency: time.Since(start)}, fmt.Errorf("smoke check timed out after %s: %w", timeout, ErrServer)
	}

	result := SmokeResult{
		OK:      reply != "",
		Reply:   smokePreview(reply),
		Latency: time.Since(start),
	}
	if !result.OK {
		return result, fmt.Errorf("smoke check: provider returned no text: %w", ErrServer)
	}
	return result, nil
}

// drainEvents consumes any remaining events so the provider's streaming
// goroutine can finish and close the channel without blocking on a send.
func drainEvents(events <-chan Event) {
	for range events {
	}
}

// smokePreview trims the collected reply to a single short line so it can be
// shown inline without flooding the caller's output.
func smokePreview(reply string) string {
	const max = 80
	out := make([]rune, 0, max)
	for _, r := range reply {
		if r == '\n' || r == '\r' {
			r = ' '
		}
		out = append(out, r)
		if len(out) >= max {
			out = append(out, '…')
			break
		}
	}
	return string(out)
}
