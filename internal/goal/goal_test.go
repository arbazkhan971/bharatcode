package goal

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsComplete(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"exact", "GOAL_COMPLETE", true},
		{"trailing line", "all checks pass, all done.\nGOAL_COMPLETE", true},
		{"with surrounding space", "  GOAL_COMPLETE  ", true},
		{"word boundary suffix", "GOAL_COMPLETED", true},
		{"inline after period", "done. GOAL_COMPLETE", true},
		{"embedded left no boundary", "MEGAGOAL_COMPLETE", false},
		{"lowercase does not count", "goal_complete", false},
		{"blocked is not complete", "GOAL_BLOCKED need key", false},
		{"empty", "", false},
		{"plain progress", "still working on it", false},
		{"mid-text mention not at end", "I will print GOAL_COMPLETE when finished.\nStill working on it now.", false},
		{"sentinel line then trailing blank lines", "all done.\nGOAL_COMPLETE\n\n  \n", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, IsComplete(tt.in))
		})
	}
}

func TestIsBlocked(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"exact", "GOAL_BLOCKED", true},
		{"with reason", "GOAL_BLOCKED: missing API credentials", true},
		{"trailing line", "I cannot proceed.\nGOAL_BLOCKED need the user", true},
		{"embedded left no boundary", "XGOAL_BLOCKED", false},
		{"complete is not blocked", "GOAL_COMPLETE", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, IsBlocked(tt.in))
		})
	}
}

func TestStrip(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"removes complete line", "all done.\nGOAL_COMPLETE", "all done."},
		{"removes complete line with trailing blank", "all done.\nGOAL_COMPLETE\n\n", "all done."},
		{"removes blocked-only line", "could not finish\nGOAL_BLOCKED", "could not finish"},
		{"no sentinel unchanged", "just some text", "just some text"},
		{"keeps reason but drops token on blocked line", "stuck\nGOAL_BLOCKED need an API key", "stuck\nneed an API key"},
		{"empty stays empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, Strip(tt.in))
		})
	}
}

func TestStripKeepsReasonButRemovesPureSentinelLine(t *testing.T) {
	// A pure sentinel line is removed entirely.
	require.Equal(t, "work so far", Strip("work so far\nGOAL_COMPLETE"))
	// The Strip result must no longer signal completion when the sentinel was
	// alone on its line.
	require.False(t, IsComplete(Strip("work so far\nGOAL_COMPLETE")))
}

func TestKickoffPrompt(t *testing.T) {
	const g = "ship the release notes for v1.2"
	p := KickoffPrompt(g)
	require.Contains(t, p, g)
	require.Contains(t, p, DoneSentinel)
	require.Contains(t, p, BlockedSentinel)
	require.True(t, strings.Contains(p, "autonomous"))
}

func TestContinuePrompt(t *testing.T) {
	const g = "migrate the config package to the new schema"
	p := ContinuePrompt(g)
	require.Contains(t, p, g)
	require.Contains(t, p, DoneSentinel)
	require.Contains(t, p, BlockedSentinel)
}

func TestOutcomeString(t *testing.T) {
	require.Equal(t, "achieved", Achieved.String())
	require.Equal(t, "blocked", Blocked.String())
	require.Equal(t, "max_iterations", MaxIterations.String())
	require.Equal(t, "stalled", Stalled.String())
	require.Equal(t, "errored", Errored.String())
	require.Equal(t, "cancelled", Cancelled.String())
	require.Equal(t, "unknown", Outcome(99).String())
}
