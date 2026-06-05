package diffutil

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnifiedIdenticalReturnsEmpty(t *testing.T) {
	require.Equal(t, "", Unified("same\ntext\n", "same\ntext\n"))
	require.Equal(t, "", Unified("", ""))
}

func TestUnifiedShowsChangedLineWithContext(t *testing.T) {
	before := "alpha\nbeta\ngamma\ndelta\n"
	after := "alpha\nBETA\ngamma\ndelta\n"

	got := Unified(before, after)

	// No ---/+++ filename header; just hunk + content.
	require.NotContains(t, got, "---")
	require.NotContains(t, got, "+++")
	require.Contains(t, got, "@@")
	require.Contains(t, got, "-beta")
	require.Contains(t, got, "+BETA")
	// Unchanged neighbours appear as space-prefixed context lines.
	require.Contains(t, got, " alpha")
	require.Contains(t, got, " gamma")
}

func TestUnifiedHandlesAddedAndRemovedLines(t *testing.T) {
	before := "one\ntwo\n"
	after := "one\ntwo\nthree\n"

	got := Unified(before, after)
	require.Contains(t, got, "+three")
	require.NotContains(t, got, "-three")
}

func TestUnifiedClampsLargeDiff(t *testing.T) {
	// A wholesale rewrite produces 2*N changed lines; ensure the body is capped
	// and the truncation notice is appended.
	var before, after strings.Builder
	for i := 0; i < 500; i++ {
		before.WriteString("old line\n")
		after.WriteString("new line\n")
	}

	got := Unified(before.String(), after.String())
	lines := strings.Split(got, "\n")
	require.LessOrEqual(t, len(lines), maxBodyLines+1)
	require.Contains(t, got, "more diff line(s) omitted")
}
