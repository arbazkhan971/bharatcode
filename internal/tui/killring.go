package tui

// Emacs-style kill-ring for the prompt. A "kill" (delete-into-ring) does not
// just discard text the way Backspace does — it saves it so a later "yank"
// (C-y / paste-from-ring) can bring it back, and consecutive kills accumulate
// into one ring entry so killing several words in a row yanks them back together.
// "yank-pop" (M-y) then cycles a just-yanked entry through the older ring entries,
// so a phrase killed earlier can be recovered without retyping. This is the
// readline/Emacs editing model BharatCode brings to the input line, layered over
// the existing undo stack (which walks edits) rather than replacing it.

// maxKillRing bounds how many distinct kill entries are retained. Older entries
// fall off the back once the cap is reached, so a long session cannot grow the
// ring without bound. The cap is generous: a user rarely cycles past a handful
// of recent kills, but the ring is cheap to keep.
const maxKillRing = 60

// killRing holds the recent kills, newest first (entries[0] is the most recent),
// plus the cursor used by yank-pop to cycle through them. It lives on the line
// editor and is mutated only from the UI goroutine, so it needs no locking.
type killRing struct {
	// entries are the killed strings, newest first. A fresh kill is pushed to the
	// front; accumulating kills extend entries[0] in place.
	entries []string
	// yankIndex points at the entry the last yank inserted, so yank-pop can step
	// to the next older entry. It is meaningful only immediately after a yank or
	// yank-pop; a fresh kill resets it to the front.
	yankIndex int
}

// push records a brand-new kill at the front of the ring, becoming the entry the
// next yank inserts. Empty text is ignored so a no-op kill (e.g. C-w at the start
// of the line) never pushes a blank entry that would later yank nothing. The ring
// is capped at maxKillRing, dropping the oldest entries.
func (k *killRing) push(text string) {
	if text == "" {
		return
	}
	k.entries = append([]string{text}, k.entries...)
	if len(k.entries) > maxKillRing {
		k.entries = k.entries[:maxKillRing]
	}
	k.yankIndex = 0
}

// appendToTop extends the most recent kill by appending text after it, so a run
// of forward kills (M-d, C-k) reads back in the order it was killed. With an
// empty ring it behaves like push, seeding the first entry. Empty text is a no-op.
func (k *killRing) appendToTop(text string) {
	if text == "" {
		return
	}
	if len(k.entries) == 0 {
		k.push(text)
		return
	}
	k.entries[0] += text
	k.yankIndex = 0
}

// prependToTop extends the most recent kill by inserting text before it, so a run
// of backward kills (C-w) reads back in left-to-right order rather than reversed.
// With an empty ring it behaves like push. Empty text is a no-op.
func (k *killRing) prependToTop(text string) {
	if text == "" {
		return
	}
	if len(k.entries) == 0 {
		k.push(text)
		return
	}
	k.entries[0] = text + k.entries[0]
	k.yankIndex = 0
}

// current returns the entry a yank would insert — the one at yankIndex — and ok
// reporting whether the ring has anything to yank. yankIndex is left untouched so
// a plain yank repeated without a pop re-inserts the same text.
func (k *killRing) current() (string, bool) {
	if len(k.entries) == 0 {
		return "", false
	}
	if k.yankIndex < 0 || k.yankIndex >= len(k.entries) {
		k.yankIndex = 0
	}
	return k.entries[k.yankIndex], true
}

// rotate advances the yank cursor to the next older entry (wrapping past the
// oldest back to the newest) and returns it, so successive yank-pops cycle the
// whole ring. It returns ok=false only when the ring is empty or holds a single
// entry (nothing to cycle to).
func (k *killRing) rotate() (string, bool) {
	if len(k.entries) <= 1 {
		return "", false
	}
	k.yankIndex = (k.yankIndex + 1) % len(k.entries)
	return k.entries[k.yankIndex], true
}
