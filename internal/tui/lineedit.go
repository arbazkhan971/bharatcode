package tui

import (
	"strings"
	"unicode"
)

// lineEditor is the canonical prompt buffer. It replaces the bare
// strings.Builder the model used before: it stores the prompt text and an
// interior cursor position (a rune index), so the input supports mid-line
// editing — word navigation (M-b/M-f), kill-and-yank (the Emacs kill-ring), and
// cursor-relative insert/delete — rather than only appending at the end.
//
// It deliberately keeps a strings.Builder-compatible surface (String, Len,
// Reset, WriteString, WriteByte) so the existing call sites that treated the
// prompt as an append-only builder keep working unchanged: those methods append
// at the end and leave the cursor there, exactly the old behavior. The
// cursor-aware methods below are the new editing vocabulary the key handler
// reaches for. The text is held as []rune so every offset is a rune index and
// multi-byte input is edited by character, not byte.
type lineEditor struct {
	runes  []rune
	cursor int // rune index in [0, len(runes)]
	ring   killRing
	// lastKill records that the previous edit was a kill, so a consecutive kill
	// accumulates into the same ring entry instead of starting a new one. Any
	// non-kill edit clears it.
	lastKill bool
	// yankAnchor and yankLen describe the span the most recent yank inserted, so
	// yank-pop (M-y) can replace it with the next ring entry. yankActive gates
	// yank-pop to the moment right after a yank; any other edit clears it.
	yankActive bool
	yankAnchor int
	yankLen    int
}

// String returns the buffer contents. It mirrors strings.Builder.String so the
// many call sites reading m.input.String() are unaffected.
func (e *lineEditor) String() string { return string(e.runes) }

// Len returns the buffer length in bytes, mirroring strings.Builder.Len so
// existing emptiness checks (Len() == 0) and length assertions read the same
// value they did against the builder.
func (e *lineEditor) Len() int { return len(string(e.runes)) }

// RuneLen returns the buffer length in runes — the unit the cursor is measured
// in.
func (e *lineEditor) RuneLen() int { return len(e.runes) }

// Cursor returns the cursor's rune index.
func (e *lineEditor) Cursor() int { return e.cursor }

// Reset clears the buffer and the cursor, mirroring strings.Builder.Reset. The
// kill-ring survives a reset (kills outlive the line they came from, the way the
// system clipboard outlives a cleared field) but the kill/yank sequence state is
// ended so the next kill starts fresh.
func (e *lineEditor) Reset() {
	e.runes = e.runes[:0]
	e.cursor = 0
	e.endSequence()
}

// WriteString appends s at the end of the buffer and moves the cursor to the
// end, matching strings.Builder.WriteString (signature and append semantics) so
// legacy callers — setInput, paste, test setup — behave exactly as before. It
// always succeeds; the error is part of the builder-compatible signature.
func (e *lineEditor) WriteString(s string) (int, error) {
	e.runes = append(e.runes, []rune(s)...)
	e.cursor = len(e.runes)
	e.endSequence()
	return len(s), nil
}

// WriteByte appends a single byte at the end and moves the cursor to the end,
// matching strings.Builder.WriteByte. It is used for the literal-newline edit
// (Alt+Enter); a byte is always a valid append here.
func (e *lineEditor) WriteByte(c byte) error {
	_, err := e.WriteString(string(rune(c)))
	return err
}

// setText replaces the whole buffer with s and parks the cursor at the end. It
// is the cursor-aware equivalent of Reset+WriteString used by setInput and
// recall, where the edit is a wholesale replacement rather than a point edit.
func (e *lineEditor) setText(s string) {
	e.runes = []rune(s)
	e.cursor = len(e.runes)
	e.endSequence()
}

// endSequence ends any in-progress kill or yank sequence, so the next kill
// starts a new ring entry and yank-pop is disabled until the next yank. Every
// non-kill, non-yank edit calls it.
func (e *lineEditor) endSequence() {
	e.lastKill = false
	e.yankActive = false
}

// --- cursor-relative editing ---

// insert writes s at the cursor and advances the cursor past it. This is the
// edit a printed character or a paste performs; with the cursor at the end it is
// identical to an append.
func (e *lineEditor) insert(s string) {
	r := []rune(s)
	if len(r) == 0 {
		return
	}
	e.runes = append(e.runes[:e.cursor], append(r, e.runes[e.cursor:]...)...)
	e.cursor += len(r)
	e.endSequence()
}

// backspace deletes the rune immediately before the cursor (the standard
// Backspace edit) and is a no-op at the start of the buffer. With the cursor at
// the end it deletes the last rune, matching the old append-only behavior.
func (e *lineEditor) backspace() {
	if e.cursor == 0 {
		return
	}
	e.runes = append(e.runes[:e.cursor-1], e.runes[e.cursor:]...)
	e.cursor--
	e.endSequence()
}

// deleteForward deletes the rune at the cursor (the C-d / Delete edit) and is a
// no-op at the end of the buffer.
func (e *lineEditor) deleteForward() {
	if e.cursor >= len(e.runes) {
		return
	}
	e.runes = append(e.runes[:e.cursor], e.runes[e.cursor+1:]...)
	e.endSequence()
}

// --- cursor motion ---

// home moves the cursor to the start of the buffer.
func (e *lineEditor) home() { e.cursor = 0; e.endSequence() }

// end moves the cursor to the end of the buffer.
func (e *lineEditor) end() { e.cursor = len(e.runes); e.endSequence() }

// left moves the cursor one rune toward the start, clamped at 0.
func (e *lineEditor) left() {
	if e.cursor > 0 {
		e.cursor--
	}
	e.endSequence()
}

// right moves the cursor one rune toward the end, clamped at the length.
func (e *lineEditor) right() {
	if e.cursor < len(e.runes) {
		e.cursor++
	}
	e.endSequence()
}

// wordLeft moves the cursor to the start of the previous word (M-b): it skips any
// run of non-word runes immediately before the cursor, then skips the word runes,
// landing on the word's first rune. At the start of the buffer it stays put.
func (e *lineEditor) wordLeft() {
	e.cursor = wordBackward(e.runes, e.cursor)
	e.endSequence()
}

// wordRight moves the cursor to the end of the next word (M-f): it skips any run
// of non-word runes at the cursor, then skips the word runes, landing just past
// the word. At the end of the buffer it stays put.
func (e *lineEditor) wordRight() {
	e.cursor = wordForward(e.runes, e.cursor)
	e.endSequence()
}

// --- kills (delete into the ring) ---

// killWordBackward deletes from the start of the previous word up to the cursor
// (the Emacs backward-kill-word, M-DEL), saving the removed text to the ring.
// Consecutive backward kills prepend so the recovered text stays in reading
// order. It is a no-op at the start of the buffer.
func (e *lineEditor) killWordBackward() {
	start := wordBackward(e.runes, e.cursor)
	if start == e.cursor {
		return
	}
	killed := string(e.runes[start:e.cursor])
	e.runes = append(e.runes[:start], e.runes[e.cursor:]...)
	e.cursor = start
	e.recordKill(killed, true)
}

// killWordForward deletes from the cursor to the end of the next word (the Emacs
// kill-word, M-d), saving the removed text to the ring. Consecutive forward kills
// append so the recovered text stays in reading order. It is a no-op at the end
// of the buffer.
func (e *lineEditor) killWordForward() {
	end := wordForward(e.runes, e.cursor)
	if end == e.cursor {
		return
	}
	killed := string(e.runes[e.cursor:end])
	e.runes = append(e.runes[:e.cursor], e.runes[end:]...)
	e.recordKill(killed, false)
}

// killLineForward deletes from the cursor to the end of the buffer (the Emacs
// kill-line, C-k), saving the removed text to the ring as a forward kill. It is a
// no-op at the end of the buffer.
func (e *lineEditor) killLineForward() {
	if e.cursor >= len(e.runes) {
		return
	}
	killed := string(e.runes[e.cursor:])
	e.runes = e.runes[:e.cursor]
	e.recordKill(killed, false)
}

// killWholeLine deletes the entire buffer (the readline unix-line-discard the
// prompt binds to Ctrl+U), saving the removed text to the ring so even a
// cleared-by-mistake line can be yanked back. It is a no-op on an empty buffer.
func (e *lineEditor) killWholeLine() {
	if len(e.runes) == 0 {
		return
	}
	killed := string(e.runes)
	e.runes = e.runes[:0]
	e.cursor = 0
	e.recordKill(killed, false)
}

// killWordBackwardUnix deletes the trailing word before the cursor using
// whitespace-only word boundaries (the readline unix-word-rubout the prompt binds
// to Alt+Backspace), keeping the whitespace that preceded the word — distinct
// from the Emacs killWordBackward, which also treats punctuation as a boundary.
// The removed text is saved to the ring as a backward kill. Text after the cursor
// is preserved, so with the cursor mid-line only the word before it is rubbed out.
// It is a no-op when there is no word before the cursor.
func (e *lineEditor) killWordBackwardUnix() {
	before := string(e.runes[:e.cursor])
	kept := deleteLastWord(before)
	if kept == before {
		return
	}
	killed := before[len(kept):]
	keptRunes := []rune(kept)
	e.runes = append(keptRunes, e.runes[e.cursor:]...)
	e.cursor = len(keptRunes)
	e.recordKill(killed, true)
}

// recordKill saves killed to the ring, accumulating into the current entry when
// the previous edit was also a kill (prepending for a backward kill so the text
// reads left-to-right, appending for a forward kill), and starting a fresh entry
// otherwise. It marks the kill sequence active so the next kill accumulates.
func (e *lineEditor) recordKill(killed string, backward bool) {
	switch {
	case !e.lastKill:
		e.ring.push(killed)
	case backward:
		e.ring.prependToTop(killed)
	default:
		e.ring.appendToTop(killed)
	}
	e.lastKill = true
	e.yankActive = false
}

// --- yanks (insert from the ring) ---

// yank inserts the most recent kill at the cursor (Emacs C-y) and records the
// inserted span so a following yank-pop can replace it. It reports ok=false when
// the ring is empty.
func (e *lineEditor) yank() bool {
	text, ok := e.ring.current()
	if !ok {
		return false
	}
	anchor := e.cursor
	r := []rune(text)
	e.runes = append(e.runes[:e.cursor], append(r, e.runes[e.cursor:]...)...)
	e.cursor += len(r)
	e.lastKill = false
	e.yankActive = true
	e.yankAnchor = anchor
	e.yankLen = len(r)
	return true
}

// yankPop replaces the text the last yank inserted with the next older ring entry
// (Emacs M-y), cycling through the ring on repeated presses. It is valid only
// immediately after a yank or another yank-pop; otherwise, and when the ring has
// nothing else to cycle to, it reports ok=false and leaves the buffer untouched.
func (e *lineEditor) yankPop() bool {
	if !e.yankActive {
		return false
	}
	text, ok := e.ring.rotate()
	if !ok {
		return false
	}
	r := []rune(text)
	end := e.yankAnchor + e.yankLen
	e.runes = append(e.runes[:e.yankAnchor], append(r, e.runes[end:]...)...)
	e.cursor = e.yankAnchor + len(r)
	e.yankLen = len(r)
	e.yankActive = true
	return true
}

// --- word-boundary helpers ---

// isWordRune reports whether r is part of an Emacs "word" for navigation and
// word-kills: a letter or digit. Everything else (space, punctuation, slashes)
// is a boundary, so M-f/M-b and M-d step token-by-token through a path or
// sentence the way they do in a readline prompt.
func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

// wordBackward returns the index of the start of the word at or before cursor:
// it skips non-word runes immediately before the cursor, then the word runes,
// stopping at the word's first rune (or 0). It is the shared core of wordLeft and
// killWordBackward so navigation and kill agree on where a word begins.
func wordBackward(runes []rune, cursor int) int {
	i := cursor
	for i > 0 && !isWordRune(runes[i-1]) {
		i--
	}
	for i > 0 && isWordRune(runes[i-1]) {
		i--
	}
	return i
}

// wordForward returns the index just past the end of the word at or after cursor:
// it skips non-word runes at the cursor, then the word runes, stopping past the
// word's last rune (or len). It is the shared core of wordRight and
// killWordForward.
func wordForward(runes []rune, cursor int) int {
	i := cursor
	n := len(runes)
	for i < n && !isWordRune(runes[i]) {
		i++
	}
	for i < n && isWordRune(runes[i]) {
		i++
	}
	return i
}

// cursorRowCol converts the editor's rune-index cursor into a (row, column) pair
// over the buffer's logical lines, so the rendering layer can place the textarea
// cursor at the matching position. Rows are split on '\n'; the column is the rune
// offset within the row. A cursor past the end clamps to the final position.
func (e *lineEditor) cursorRowCol() (row, col int) {
	if e.cursor <= 0 {
		return 0, 0
	}
	limit := e.cursor
	if limit > len(e.runes) {
		limit = len(e.runes)
	}
	col = 0
	for i := 0; i < limit; i++ {
		if e.runes[i] == '\n' {
			row++
			col = 0
		} else {
			col++
		}
	}
	return row, col
}

// trimmedValue returns the buffer with surrounding ASCII whitespace removed, used
// where the old code called strings.TrimSpace(m.input.String()).
func (e *lineEditor) trimmedValue() string {
	return strings.TrimSpace(e.String())
}
