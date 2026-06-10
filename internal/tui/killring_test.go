package tui

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestKillRing_PushAndCurrent proves a pushed kill becomes the entry a yank
// would insert, and an empty ring reports nothing to yank.
func TestKillRing_PushAndCurrent(t *testing.T) {
	t.Parallel()
	var k killRing
	_, ok := k.current()
	require.False(t, ok, "empty ring has nothing to yank")

	k.push("hello")
	got, ok := k.current()
	require.True(t, ok)
	require.Equal(t, "hello", got)
}

// TestKillRing_PushIgnoresEmpty proves a no-op kill never pushes a blank entry
// that would later yank nothing.
func TestKillRing_PushIgnoresEmpty(t *testing.T) {
	t.Parallel()
	var k killRing
	k.push("")
	_, ok := k.current()
	require.False(t, ok)
}

// TestKillRing_NewestFirst proves the most recent push is yanked first.
func TestKillRing_NewestFirst(t *testing.T) {
	t.Parallel()
	var k killRing
	k.push("first")
	k.push("second")
	got, _ := k.current()
	require.Equal(t, "second", got)
}

// TestKillRing_AppendToTop proves a forward-kill accumulation extends the newest
// entry in reading order.
func TestKillRing_AppendToTop(t *testing.T) {
	t.Parallel()
	var k killRing
	k.push("foo")
	k.appendToTop(" bar")
	got, _ := k.current()
	require.Equal(t, "foo bar", got)
}

// TestKillRing_PrependToTop proves a backward-kill accumulation extends the
// newest entry so the recovered text stays left-to-right.
func TestKillRing_PrependToTop(t *testing.T) {
	t.Parallel()
	var k killRing
	k.push("bar")
	k.prependToTop("foo ")
	got, _ := k.current()
	require.Equal(t, "foo bar", got)
}

// TestKillRing_AccumulateOnEmptySeeds proves append/prepend on an empty ring seed
// the first entry rather than dropping the text.
func TestKillRing_AccumulateOnEmptySeeds(t *testing.T) {
	t.Parallel()
	var a, b killRing
	a.appendToTop("x")
	got, ok := a.current()
	require.True(t, ok)
	require.Equal(t, "x", got)

	b.prependToTop("y")
	got, ok = b.current()
	require.True(t, ok)
	require.Equal(t, "y", got)
}

// TestKillRing_Rotate proves yank-pop cycles through the entries newest→older and
// wraps back to the newest.
func TestKillRing_Rotate(t *testing.T) {
	t.Parallel()
	var k killRing
	k.push("a") // oldest
	k.push("b")
	k.push("c") // newest

	got, _ := k.current()
	require.Equal(t, "c", got)

	got, ok := k.rotate()
	require.True(t, ok)
	require.Equal(t, "b", got)

	got, _ = k.rotate()
	require.Equal(t, "a", got)

	got, _ = k.rotate()
	require.Equal(t, "c", got, "rotate wraps back to the newest")
}

// TestKillRing_RotateSingleEntry proves yank-pop has nothing to cycle to when the
// ring holds one or zero entries.
func TestKillRing_RotateSingleEntry(t *testing.T) {
	t.Parallel()
	var k killRing
	_, ok := k.rotate()
	require.False(t, ok)

	k.push("only")
	_, ok = k.rotate()
	require.False(t, ok)
}

// TestKillRing_PushResetsYankCursor proves a fresh kill after a rotate brings the
// yank cursor back to the front, so the next yank inserts the newest kill.
func TestKillRing_PushResetsYankCursor(t *testing.T) {
	t.Parallel()
	var k killRing
	k.push("a")
	k.push("b")
	_, _ = k.rotate() // cursor now on "a"

	k.push("c")
	got, _ := k.current()
	require.Equal(t, "c", got, "a new kill is yanked first")
}

// TestKillRing_Cap proves the ring never grows past maxKillRing entries.
func TestKillRing_Cap(t *testing.T) {
	t.Parallel()
	var k killRing
	for i := 0; i < maxKillRing+10; i++ {
		k.push(string(rune('a' + i%26)))
	}
	require.Len(t, k.entries, maxKillRing)
}
