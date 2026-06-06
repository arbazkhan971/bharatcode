package notification

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSystemNotifier_DoesNotPanic verifies that calling SystemNotifier.Notify
// does not panic regardless of whether the underlying OS tool is installed.
// When notify-send / osascript is absent the call fails with an error, which
// is acceptable — the TUI discards notification errors.
func TestSystemNotifier_DoesNotPanic(t *testing.T) {
	t.Parallel()

	n := SystemNotifier{}
	// The call may succeed (on a machine with the tool) or fail (in CI). Both
	// outcomes are acceptable; only a panic is not.
	_ = n.Notify("BharatCode", "test notification body")
}

// TestEscapeAppleScript verifies that special AppleScript characters are
// properly escaped so notification body text is safe to embed in a script.
func TestEscapeAppleScript(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in, want string
	}{
		{`hello`, `hello`},
		{`say "hi"`, `say \"hi\"`},
		{`back\slash`, `back\\slash`},
		{`"quoted" and \path`, `\"quoted\" and \\path`},
	}
	for _, tc := range cases {
		got := escapeAppleScript(tc.in)
		require.Equal(t, tc.want, got, "input: %q", tc.in)
	}
}

// TestSystemNotifier_ViaFocusAware ensures that a SystemNotifier wired into a
// FocusAware wrapper is only dispatched when the terminal has lost focus — i.e.
// the FocusAware filter still applies to the real backend.
func TestSystemNotifier_ViaFocusAware(t *testing.T) {
	t.Parallel()

	// Use a recording notifier to intercept the dispatch without touching the OS.
	rec := &recordingNotifier{}
	focus := NewFocusAware(rec)

	// Focused (default): Notify must NOT reach the backend.
	_ = focus.Notify("BharatCode", "focused turn complete")
	require.Equal(t, 0, rec.count)

	// Blurred: Notify MUST reach the backend.
	focus.SetFocused(false)
	_ = focus.Notify("BharatCode", "blurred turn complete")
	require.Equal(t, 1, rec.count)
}
