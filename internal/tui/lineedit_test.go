package tui

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// newEditor returns a lineEditor seeded with value and the cursor parked at the
// end, the state setText/WriteString leave it in.
func newEditor(value string) *lineEditor {
	e := &lineEditor{}
	e.setText(value)
	return e
}

// TestLineEditor_BuilderSurface proves the strings.Builder-compatible methods
// keep their append-and-end-cursor behavior, so legacy call sites are unaffected.
func TestLineEditor_BuilderSurface(t *testing.T) {
	t.Parallel()
	var e lineEditor
	_, _ = e.WriteString("héllo")
	require.Equal(t, "héllo", e.String())
	require.Equal(t, len("héllo"), e.Len(), "Len is byte length like strings.Builder")
	require.Equal(t, 5, e.RuneLen())
	require.Equal(t, 5, e.Cursor(), "WriteString parks the cursor at the end")

	_ = e.WriteByte('!')
	require.Equal(t, "héllo!", e.String())
	require.Equal(t, 6, e.Cursor())

	e.Reset()
	require.Equal(t, "", e.String())
	require.Equal(t, 0, e.Cursor())
}

// TestLineEditor_InsertAtCursor proves insert places text at the cursor and
// advances past it, so mid-line typing works rather than only appends.
func TestLineEditor_InsertAtCursor(t *testing.T) {
	t.Parallel()
	e := newEditor("ac")
	e.left() // cursor between a and c
	e.insert("b")
	require.Equal(t, "abc", e.String())
	require.Equal(t, 2, e.Cursor())
}

// TestLineEditor_BackspaceAndDeleteForward proves the two single-rune deletes
// remove before and at the cursor respectively, clamping at the ends.
func TestLineEditor_BackspaceAndDeleteForward(t *testing.T) {
	t.Parallel()
	e := newEditor("abc")
	e.backspace()
	require.Equal(t, "ab", e.String())

	e.home()
	e.deleteForward()
	require.Equal(t, "b", e.String())

	e.home()
	e.backspace() // no-op at start
	require.Equal(t, "b", e.String())
	e.end()
	e.deleteForward() // no-op at end
	require.Equal(t, "b", e.String())
}

// TestLineEditor_CursorMotion proves home/end/left/right move and clamp the
// cursor as expected.
func TestLineEditor_CursorMotion(t *testing.T) {
	t.Parallel()
	e := newEditor("abc")
	require.Equal(t, 3, e.Cursor())
	e.home()
	require.Equal(t, 0, e.Cursor())
	e.left()
	require.Equal(t, 0, e.Cursor(), "left clamps at the start")
	e.right()
	require.Equal(t, 1, e.Cursor())
	e.end()
	require.Equal(t, 3, e.Cursor())
	e.right()
	require.Equal(t, 3, e.Cursor(), "right clamps at the end")
}

// TestLineEditor_WordNav proves M-b/M-f step token-by-token over whitespace and
// punctuation, the readline word-navigation.
func TestLineEditor_WordNav(t *testing.T) {
	t.Parallel()
	e := newEditor("foo bar.baz")
	// From the end, word-left lands on the start of each word in turn.
	e.wordLeft()
	require.Equal(t, 8, e.Cursor(), "start of 'baz'")
	e.wordLeft()
	require.Equal(t, 4, e.Cursor(), "skips '.' to start of 'bar'")
	e.wordLeft()
	require.Equal(t, 0, e.Cursor(), "start of 'foo'")
	e.wordLeft()
	require.Equal(t, 0, e.Cursor(), "clamps at the start")

	// Forward from the start.
	e.wordRight()
	require.Equal(t, 3, e.Cursor(), "end of 'foo'")
	e.wordRight()
	require.Equal(t, 7, e.Cursor(), "end of 'bar'")
}

// TestLineEditor_KillWordForward proves M-d kills the next word into the ring and
// that a following yank brings it back.
func TestLineEditor_KillWordForward(t *testing.T) {
	t.Parallel()
	e := newEditor("hello world")
	e.home()
	e.killWordForward()
	require.Equal(t, " world", e.String())

	e.end()
	require.True(t, e.yank())
	require.Equal(t, " worldhello", e.String())
}

// TestLineEditor_KillWordBackward proves the Emacs backward-kill-word removes the
// word before the cursor, treating punctuation as a boundary.
func TestLineEditor_KillWordBackward(t *testing.T) {
	t.Parallel()
	e := newEditor("foo bar.baz")
	e.killWordBackward()
	require.Equal(t, "foo bar.", e.String())
	e.killWordBackward()
	require.Equal(t, "foo ", e.String(), "punctuation is a word boundary")
}

// TestLineEditor_KillLineForward proves C-k-style kill removes from the cursor to
// the end of the line.
func TestLineEditor_KillLineForward(t *testing.T) {
	t.Parallel()
	e := newEditor("keep this")
	e.home()
	e.wordRight() // after "keep"
	e.killLineForward()
	require.Equal(t, "keep", e.String())
	require.True(t, e.yank())
	require.Equal(t, "keep this", e.String())
}

// TestLineEditor_KillWholeLine proves Ctrl+U-style kill clears the buffer and the
// cleared text can be yanked back.
func TestLineEditor_KillWholeLine(t *testing.T) {
	t.Parallel()
	e := newEditor("discard me")
	e.killWholeLine()
	require.Equal(t, "", e.String())
	require.True(t, e.yank())
	require.Equal(t, "discard me", e.String())
}

// TestLineEditor_KillWordBackwardUnix proves the Alt+Backspace rubout deletes the
// trailing whitespace-delimited word (keeping punctuation) and preserves any text
// after the cursor.
func TestLineEditor_KillWordBackwardUnix(t *testing.T) {
	t.Parallel()
	e := newEditor("go test ./...")
	e.killWordBackwardUnix()
	require.Equal(t, "go test ", e.String(), "whitespace is the only boundary")

	// Mid-line: only the word before the cursor is rubbed out, text after stays.
	e = newEditor("alpha beta gamma")
	for i := 0; i < 5; i++ { // move the cursor to just before "gamma"
		e.left()
	}
	e.killWordBackwardUnix()
	require.Equal(t, "alpha gamma", e.String())
}

// TestLineEditor_KillAccumulates proves consecutive kills merge into one ring
// entry in reading order — forward kills append, backward kills prepend.
func TestLineEditor_KillAccumulates(t *testing.T) {
	t.Parallel()
	// Two forward kills accumulate left-to-right.
	e := newEditor("one two three")
	e.home()
	e.killWordForward() // kills "one"
	e.killWordForward() // kills " two"
	got, ok := e.ring.current()
	require.True(t, ok)
	require.Equal(t, "one two", got)

	// A non-kill edit between kills starts a fresh ring entry.
	e2 := newEditor("one two")
	e2.home()
	e2.killWordForward() // kills "one"
	e2.right()           // a cursor motion ends the kill sequence
	e2.killWordForward() // kills "two" into a fresh ring entry
	got2, _ := e2.ring.current()
	require.NotEqual(t, "one two", got2, "a motion between kills breaks accumulation")
}

// TestLineEditor_YankPop proves M-y after a yank replaces the inserted text with
// the next older ring entry, and is inert when not preceded by a yank.
func TestLineEditor_YankPop(t *testing.T) {
	t.Parallel()
	e := &lineEditor{}
	// Seed the ring with two distinct kills via separate kill sequences.
	e.setText("aaa")
	e.killWholeLine() // ring: ["aaa"]
	e.setText("bbb")
	e.killWholeLine() // ring: ["bbb", "aaa"]

	require.False(t, e.yankPop(), "yank-pop without a preceding yank is inert")

	require.True(t, e.yank())
	require.Equal(t, "bbb", e.String(), "yank inserts the newest kill")

	require.True(t, e.yankPop())
	require.Equal(t, "aaa", e.String(), "yank-pop swaps in the next older kill")

	require.True(t, e.yankPop())
	require.Equal(t, "bbb", e.String(), "yank-pop cycles back to the newest")
}

// TestLineEditor_YankEmptyRing proves a yank with nothing killed is a no-op.
func TestLineEditor_YankEmptyRing(t *testing.T) {
	t.Parallel()
	e := newEditor("text")
	require.False(t, e.yank())
	require.Equal(t, "text", e.String())
}

// TestLineEditor_CursorRowCol proves the rune offset maps to the right (row, col)
// across logical lines, which the renderer uses to place the caret.
func TestLineEditor_CursorRowCol(t *testing.T) {
	t.Parallel()
	e := newEditor("ab\ncde")
	row, col := e.cursorRowCol()
	require.Equal(t, 1, row)
	require.Equal(t, 3, col)

	e.home()
	row, col = e.cursorRowCol()
	require.Equal(t, 0, row)
	require.Equal(t, 0, col)
}
