package tui

import (
	"github.com/arbazkhan971/bharatcode/internal/tui/styles"
	"github.com/charmbracelet/bubbles/v2/help"
	"github.com/charmbracelet/bubbles/v2/textarea"
	tea "github.com/charmbracelet/bubbletea/v2"
)

// isSubmitKey reports whether a key press should submit the prompt — true for
// the carriage-return Enter (\r), the keypad Enter, and the line-feed (\n)
// forms that PTY automation commonly sends, so a driver that "presses Enter" by
// writing either newline byte submits consistently.
//
// Why this exists: a terminal in raw mode reports the keyboard's Enter as CR
// (\r, KeyEnter), which the main key switch already routes to submitInput. But
// some PTY drivers write a bare line-feed (\n) for Enter; ultraviolet's input
// decoder turns that byte into Ctrl+J (rune 'j' + ModCtrl), not KeyEnter, so it
// would otherwise fall through unhandled and never submit. Ctrl+J has been the
// "line feed = Enter" equivalent since teletypes (and in readline), so mapping
// it — plus a synthetic bare '\n' code — onto submit is both correct and what
// automation expects. A bare LF code (0x0a) is included for drivers that inject
// the rune directly rather than through the byte decoder.
//
// This predicate is the canonical spec of the Enter forms the input dispatch
// accepts; the dispatch in handleKey must consult it before its msg.String()
// switch so the Ctrl+J/bare-LF forms (whose String() is "ctrl+j"/"\n", not
// "enter") reach submitInput instead of falling through to the default case and
// being dropped. The accepted set is deliberately the same set the runtime
// recognizes once wired — CR Enter, keypad Enter, Ctrl+J, and the synthetic LF
// code — so predicate and dispatch never disagree about whether a key submits.
//
// The Alt modifier is excluded: Alt+Enter is the multi-line newline binding, so
// it must not be treated as a submit. Other modifiers on the Enter codes
// (Shift, Ctrl) are NOT treated as submits here: the runtime switch matches
// those as their own chords ("ctrl+enter"/"shift+enter"), which it does not
// route to submit, so accepting them in the predicate would make the spec and
// the dispatch disagree. Keeping the predicate to the plain newline forms keeps
// the two in lockstep.
func isSubmitKey(msg tea.KeyPressMsg) bool {
	switch msg.Code {
	case tea.KeyEnter, tea.KeyKpEnter, '\n':
		// CR (\r/KeyEnter), keypad Enter, or a synthetic line-feed code submit only
		// when unmodified; a modifier turns Enter into a distinct chord the runtime
		// handles (or ignores) separately — Alt+Enter is the multi-line newline.
		return msg.Mod == 0
	case 'j':
		// A line-feed byte (\n) decodes to Ctrl+J; treat exactly that chord as
		// Enter so a PTY driver that writes \n submits the same as one that writes
		// \r. Any other modifier mix (e.g. Alt+J) is a different key, not a submit.
		return msg.Mod == tea.ModCtrl
	}
	return false
}

// newPromptInput builds the prompt textarea in the activity-stream style: a
// "› " prompt glyph, the muted discovery placeholder, no line numbers, and
// Enter wired to submit rather than insert a newline (multi-line prompts use
// Alt+Enter, handled in the main key switch). It replaces the hand-rolled
// append-only buffer renderer (the "▌" glyph + renderInputArea) with a real
// bubbles textarea so the prompt gets a proper cursor and word-wrap.
//
// The canonical prompt text still lives in the model's input buffer (so the
// existing history, undo/redo, recall, completion, and reverse-search wiring is
// preserved untouched); syncPromptInput mirrors that buffer into the textarea
// before each render. The virtual cursor is left on so the block cursor renders
// inline in the returned string, matching the previous "▌" look without
// threading a terminal cursor through the string-based View; the compose phase
// that owns the viewport/cursor wiring can flip VirtualCursor off and hand the
// real Cursor() to the View if it re-anchors the terminal cursor.
func newPromptInput() textarea.Model {
	ta := textarea.New()
	ta.Prompt = styles.PromptGlyph
	ta.Placeholder = inputPlaceholder
	ta.ShowLineNumbers = false
	// Enter submits the prompt; disabling the textarea's own newline binding lets
	// the main key switch own Enter (submit) and Alt+Enter (literal newline).
	ta.KeyMap.InsertNewline.SetEnabled(false)

	// Style the prompt glyph, placeholder, and text from the shared palette so
	// the input reads consistently with the rest of the activity stream. The
	// focused and blurred states share the recessive styling; focus is conveyed
	// by the surrounding frame (see renderPromptInput) and the cursor's presence.
	dark := styles.IsDarkBackground()
	st := textarea.DefaultStyles(dark)
	st.Focused.Prompt = styles.Accent
	st.Blurred.Prompt = styles.Muted
	st.Focused.Placeholder = styles.Placeholder
	st.Blurred.Placeholder = styles.Placeholder
	st.Focused.Text = styles.Primary
	st.Blurred.Text = styles.Primary
	// Keep the cursor line flush with the rest of the prompt rather than
	// reverse-highlighted, so a single-line prompt reads as a plain input line.
	st.Focused.CursorLine = styles.Primary
	ta.Styles = st

	// A single visible row keeps the input flush in the layout's input region
	// while still wrapping long prompts; the buffer itself may hold more.
	ta.SetHeight(1)
	return ta
}

// syncPromptInput mirrors the canonical prompt buffer (value) into the textarea
// and reconciles focus and width before rendering. The model keeps value as its
// source of truth (so history/undo/recall/completion stay authoritative); this
// pushes the latest text and cursor-relevant state into the widget so its View
// reflects the buffer. Focus is toggled so the cursor only shows when the prompt
// holds focus, matching the previous "▌"-when-focused behavior.
func syncPromptInput(ta textarea.Model, value string, cursor int, focused bool, width int) textarea.Model {
	if w := promptInputWidth(width); w > 0 {
		ta.SetWidth(w)
	}
	if ta.Value() != value {
		ta.SetValue(value)
	}
	// Position the textarea cursor to mirror the editor's interior cursor, so
	// word-navigation and mid-line kills/yanks show the caret where edits will
	// land rather than always at the end. cursor is a rune offset into value;
	// place() converts it to the textarea's (row, column).
	placeTextareaCursor(&ta, value, cursor)
	if focused {
		if !ta.Focused() {
			ta.Focus()
		}
		// Advertise the discovery affordances on an empty focused prompt only,
		// matching the previous behavior where the placeholder was gated on input
		// focus so a focused-elsewhere view stays uncluttered.
		ta.Placeholder = inputPlaceholder
	} else {
		if ta.Focused() {
			ta.Blur()
		}
		ta.Placeholder = ""
	}
	return ta
}

// placeTextareaCursor moves the textarea's caret to the rune offset cursor into
// value, converting the flat offset to the (row, column) the textarea addresses.
// It walks the widget to the top-left, steps down to the target row, then sets
// the column — the public textarea API exposes only relative row moves and an
// absolute column set, so the move is composed from those. A cursor past the end
// clamps to the final character, matching the editor's own clamping.
func placeTextareaCursor(ta *textarea.Model, value string, cursor int) {
	runes := []rune(value)
	if cursor > len(runes) {
		cursor = len(runes)
	}
	if cursor < 0 {
		cursor = 0
	}
	row, col := 0, 0
	for i := 0; i < cursor; i++ {
		if runes[i] == '\n' {
			row++
			col = 0
		} else {
			col++
		}
	}
	// Walk to the first row, then descend to the target row. CursorUp/CursorDown
	// clamp at the ends, so over-stepping is safe.
	for i := 0; i < ta.LineCount(); i++ {
		ta.CursorUp()
	}
	for i := 0; i < row; i++ {
		ta.CursorDown()
	}
	ta.SetCursorColumn(col)
}

// promptInputWidth derives the textarea's content width from the terminal width,
// reserving a column on each side so the cursor and prompt never sit against the
// screen edge. It clamps to a small positive minimum so a narrow terminal still
// renders a usable input.
func promptInputWidth(width int) int {
	w := width - 2
	if w < 1 {
		return 1
	}
	return w
}

// renderPromptInput renders the prompt line from the textarea. It is the
// textarea-backed replacement for renderInputArea; the placeholder is now owned
// by the textarea (shown on an empty focused buffer), so callers no longer
// append it manually.
func renderPromptInput(ta textarea.Model) string {
	return ta.View()
}

// renderHelpBar renders the muted footer help line from the global keymap using
// the bubbles help bubble. width bounds the line so it wraps/elides to the
// terminal; an empty result is returned for a non-positive width so the caller
// can omit the row entirely.
func renderHelpBar(h help.Model, keys keyMap, width int) string {
	if width <= 0 {
		return ""
	}
	h.Width = width
	return h.View(keys)
}

// newHelpModel builds the footer help bubble, styled from the shared palette so
// the key and description columns read as muted footer chrome rather than the
// bubble's stock grays.
func newHelpModel() help.Model {
	h := help.New()
	st := help.DefaultStyles(styles.IsDarkBackground())
	st.ShortKey = styles.Muted
	st.ShortDesc = styles.Faint
	st.ShortSeparator = styles.Faint
	st.FullKey = styles.Muted
	st.FullDesc = styles.Faint
	st.FullSeparator = styles.Faint
	st.Ellipsis = styles.Faint
	h.Styles = st
	return h
}
