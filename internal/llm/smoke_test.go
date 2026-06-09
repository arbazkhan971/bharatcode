package llm

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSmokeSucceedsWhenProviderAnswers(t *testing.T) {
	// A provider that emits text then ends cleanly is a healthy provider: the
	// probe collects the reply and reports OK.
	p := &stubProvider{
		name: "p",
		events: streamOf(
			StartEvent{Provider: "p", Model: "m"},
			DeltaTextEvent{Text: "ok"},
			EndEvent{},
		),
	}

	res, err := Smoke(context.Background(), p, "m", time.Second)
	require.NoError(t, err)
	require.True(t, res.OK)
	require.Equal(t, "ok", res.Reply)
	require.Equal(t, 1, p.calls)
}

func TestSmokeMapsStreamSetupError(t *testing.T) {
	// When Stream itself fails (e.g. a missing API key surfaced before any
	// request goes out), the sentinel is preserved so callers can give an
	// auth-specific hint.
	p := &stubProvider{name: "p", err: fmt.Errorf("no key: %w", ErrAuth)}

	res, err := Smoke(context.Background(), p, "m", time.Second)
	require.ErrorIs(t, err, ErrAuth)
	require.False(t, res.OK)
}

func TestSmokeMapsMidStreamError(t *testing.T) {
	// An ErrorEvent after the stream started (a provider 401/500 mid-flight) is
	// surfaced with its sentinel intact.
	p := &stubProvider{
		name: "p",
		events: streamOf(
			StartEvent{Provider: "p", Model: "m"},
			ErrorEvent{Err: fmt.Errorf("rejected: %w", ErrModelNotFound)},
		),
	}

	res, err := Smoke(context.Background(), p, "m", time.Second)
	require.ErrorIs(t, err, ErrModelNotFound)
	require.False(t, res.OK)
}

func TestSmokeFailsWhenNoTextProduced(t *testing.T) {
	// A stream that ends without ever emitting text did not prove the model can
	// answer, so the probe reports a server-side failure rather than a false OK.
	p := &stubProvider{
		name: "p",
		events: streamOf(
			StartEvent{Provider: "p", Model: "m"},
			EndEvent{},
		),
	}

	res, err := Smoke(context.Background(), p, "m", time.Second)
	require.ErrorIs(t, err, ErrServer)
	require.False(t, res.OK)
}

func TestSmokeRejectsNilProvider(t *testing.T) {
	_, err := Smoke(context.Background(), nil, "m", time.Second)
	require.Error(t, err)
}

func TestSmokePreviewTrimsAndFlattens(t *testing.T) {
	// Newlines collapse to spaces and a long reply is truncated so the preview
	// stays a single short line for inline display.
	p := &stubProvider{
		name: "p",
		events: streamOf(
			StartEvent{Provider: "p", Model: "m"},
			DeltaTextEvent{Text: "line one\nline two"},
			EndEvent{},
		),
	}

	res, err := Smoke(context.Background(), p, "m", time.Second)
	require.NoError(t, err)
	require.Equal(t, "line one line two", res.Reply)
}
