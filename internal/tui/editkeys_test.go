package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

// keyAlt builds an Alt+<rune> key press for the Emacs word-edit bindings.
func keyAlt(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: r, Mod: tea.ModAlt})
}

func keyLeft() tea.KeyPressMsg  { return keySpecial("left", tea.KeyLeft) }
func keyRight() tea.KeyPressMsg { return keySpecial("right", tea.KeyRight) }

// TestArrows_MoveInputCursor proves Left/Right move the prompt's interior cursor
// so editing is no longer end-only.
func TestArrows_MoveInputCursor(t *testing.T) {
	t.Parallel()
	m := newSizedModel(t)
	typeString(t, m, "abc")
	require.Equal(t, 3, m.input.Cursor())

	_, _ = m.Update(keyLeft())
	require.Equal(t, 2, m.input.Cursor())
	_, _ = m.Update(keyLeft())
	_, _ = m.Update(keyRight())
	require.Equal(t, 2, m.input.Cursor())
}

// TestInsertAtCursor_AfterLeft proves typing inserts at the moved cursor, not at
// the end of the buffer.
func TestInsertAtCursor_AfterLeft(t *testing.T) {
	t.Parallel()
	m := newSizedModel(t)
	typeString(t, m, "ac")
	_, _ = m.Update(keyLeft()) // between a and c
	typeString(t, m, "b")
	require.Equal(t, "abc", m.input.String())
}

// TestAltBF_WordNavigation proves Alt+B / Alt+F step the cursor word-by-word.
func TestAltBF_WordNavigation(t *testing.T) {
	t.Parallel()
	m := newSizedModel(t)
	typeString(t, m, "foo bar")
	_, _ = m.Update(keyAlt('b'))
	require.Equal(t, 4, m.input.Cursor(), "Alt+B lands on the start of 'bar'")
	_, _ = m.Update(keyAlt('b'))
	require.Equal(t, 0, m.input.Cursor(), "Alt+B again lands on the start of 'foo'")
	_, _ = m.Update(keyAlt('f'))
	require.Equal(t, 3, m.input.Cursor(), "Alt+F lands past 'foo'")
}

// TestAltD_KillWordForwardThenYank proves Alt+D kills the next word into the
// kill-ring and Ctrl+Y yanks it back at the cursor.
func TestAltD_KillWordForwardThenYank(t *testing.T) {
	t.Parallel()
	m := newSizedModel(t)
	typeString(t, m, "hello world")
	_, _ = m.Update(keyAlt('b')) // cursor at start of "world"
	_, _ = m.Update(keyAlt('d')) // kill "world"
	require.Equal(t, "hello ", m.input.String())

	_, _ = m.Update(keyCtrl('e')) // move to end
	_, _ = m.Update(keyCtrl('y')) // nothing to redo → yank
	require.Equal(t, "hello world", m.input.String())
}

// TestAltK_KillLineForward proves Alt+K kills from the cursor to the end of the
// line into the ring.
func TestAltK_KillLineForward(t *testing.T) {
	t.Parallel()
	m := newSizedModel(t)
	typeString(t, m, "keep this")
	_, _ = m.Update(keyAlt('b')) // start of "this"
	_, _ = m.Update(keyAlt('k')) // kill "this"
	require.Equal(t, "keep ", m.input.String())

	_, _ = m.Update(keyCtrl('y')) // yank it back
	require.Equal(t, "keep this", m.input.String())
}

// TestCtrlU_YankBack proves the line cleared by Ctrl+U can be yanked back, since
// the clear now saves to the kill-ring.
func TestCtrlU_YankBack(t *testing.T) {
	t.Parallel()
	m := newSizedModel(t)
	typeString(t, m, "important prompt")
	_, _ = m.Update(keyCtrl('u'))
	require.Equal(t, 0, m.input.Len())

	_, _ = m.Update(keyCtrl('y'))
	require.Equal(t, "important prompt", m.input.String())
}

// TestCtrlY_RedoTakesPrecedence proves Ctrl+Y still redoes a pending undone edit
// before falling through to a kill-ring yank, so the established undo/redo path is
// unchanged whenever a redo is available.
func TestCtrlY_RedoTakesPrecedence(t *testing.T) {
	t.Parallel()
	m := newSizedModel(t)
	typeString(t, m, "ab")
	_, _ = m.Update(keyCtrl('z')) // undo → "a"
	require.Equal(t, "a", m.input.String())
	_, _ = m.Update(keyCtrl('y')) // redo → "ab" (not a yank)
	require.Equal(t, "ab", m.input.String())
}

// TestAltY_YankPopCycles proves Ctrl+Y then Alt+Y cycles through the kill-ring,
// swapping the just-yanked text for the next older kill.
func TestAltY_YankPopCycles(t *testing.T) {
	t.Parallel()
	m := newSizedModel(t)
	typeString(t, m, "aaa")
	_, _ = m.Update(keyCtrl('u')) // ring: ["aaa"]
	typeString(t, m, "bbb")
	_, _ = m.Update(keyCtrl('u')) // ring: ["bbb","aaa"]

	_, _ = m.Update(keyCtrl('y')) // yank newest → "bbb"
	require.Equal(t, "bbb", m.input.String())
	_, _ = m.Update(keyAlt('y')) // pop → "aaa"
	require.Equal(t, "aaa", m.input.String())
	_, _ = m.Update(keyAlt('y')) // pop cycles back → "bbb"
	require.Equal(t, "bbb", m.input.String())
}
