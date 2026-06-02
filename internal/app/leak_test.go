package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

// TestMain runs every test in this package under goleak so any goroutine
// leaked by App construction, operation, or Close fails the suite. No ignore
// list is used: a clean App graph and the modernc.org/sqlite CGO-free driver
// leave no surviving goroutines, so every reported leak reflects a real wiring
// defect rather than a tolerated background routine.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// TestNewClose_NoLeak constructs an App and Closes it repeatedly and asserts
// no goroutine survives the final Close. New spawns long-lived goroutines (the
// pubsub topic workers), so a Close that failed to reap them would leave a
// goleak-visible leak; this test therefore guards real teardown rather than
// merely smoke-testing construction. The loop shares one tempDir so repeated
// New/Close cycles reuse one DB path and surface gross handle accumulation
// instead of masking it behind a fresh directory each iteration. The deferred
// VerifyNone runs before any t.Cleanup, so every App is Closed inline inside
// the loop rather than via Cleanup, ensuring the assertion observes a fully
// torn-down graph.
func TestNewClose_NoLeak(t *testing.T) {
	defer goleak.VerifyNone(t)

	ctx := context.Background()
	tempDir := t.TempDir()
	setAppEnv(t, tempDir)

	for i := 0; i < 5; i++ {
		a, err := New(ctx, Options{ProjectDir: tempDir})
		require.NoError(t, err, "iteration %d: New", i)
		require.NoError(t, a.Close(ctx), "iteration %d: Close", i)
	}
}
