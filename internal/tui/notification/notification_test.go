package notification

import (
	"testing"

	"github.com/stretchr/testify/require"
)

type recordingNotifier struct {
	count int
}

func (r *recordingNotifier) Notify(string, string) error {
	r.count++
	return nil
}

func TestNoopWhenTerminalFocused(t *testing.T) {
	t.Parallel()

	rec := &recordingNotifier{}
	focus := NewFocusAware(rec)
	require.NoError(t, focus.Notify("done", "focused"))
	require.Equal(t, 0, rec.count)
	require.Equal(t, 0, focus.Sent())

	focus.SetFocused(false)
	require.NoError(t, focus.Notify("done", "blurred"))
	require.Equal(t, 1, rec.count)
	require.Equal(t, 1, focus.Sent())
}
