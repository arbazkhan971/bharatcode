package tui

import (
	"fmt"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/session"
	"github.com/arbazkhan971/bharatcode/internal/tui/styles"
	tea "github.com/charmbracelet/bubbletea/v2"
	"github.com/charmbracelet/bubbles/v2/list"
	"github.com/charmbracelet/bubbles/v2/spinner"
	"github.com/charmbracelet/lipgloss/v2"
)

// This file holds the list-based model and session pickers and the streaming
// spinner. The pickers are bubbles/v2 list models: each row is a list.Item that
// carries its own filter text, so the list's built-in incremental filter
// replaces the hand-rolled three-tier match, and the delegate draws the
// selected row, pagination, and "N items" tally for free. The compose phase
// wires a picker's list into the dialog stack and forwards keys to its Update;
// this file owns the item types, list construction, selection read-back, and a
// centered-modal render so those concerns stay out of tui.go.

// pickerListHeight is the number of body rows a picker's list draws before it
// paginates. It is the inner height handed to list.New / SetSize; the modal
// border and the list's own filter/help chrome sit outside it. Ten visible rows
// keeps the modal shorter than a minimum-height (24-row) terminal while still
// showing enough of a long model or session list to orient in, and the list's
// built-in paginator reaches the rest. compose may shrink it to the live
// terminal height via setSize.
const pickerListHeight = 10

// pickerListWidth is the inner width a picker's list is built at before the
// first WindowSizeMsg resizes it to the terminal. It matches the minimum
// terminal width less the modal frame so a picker rendered before any size
// arrives (for example in a test) still wraps sensibly rather than at list's
// zero-width short-circuit.
const pickerListWidth = minWidth - 8

// ---------------------------------------------------------------------------
// Items
// ---------------------------------------------------------------------------

// modelItem adapts a configured model to a list row. active marks the row the
// session is currently using so the picker can lead it with a filled dot, the
// way the old picker flagged the in-use model with activeMarker.
type modelItem struct {
	model  config.Model
	active bool
}

// label is the "provider/id" identifier shown for the model, the same compound
// label the previous picker rendered and matched against.
func (i modelItem) label() string {
	return i.model.Provider + "/" + i.model.ID
}

// FilterValue is the text the list's incremental filter matches against. It
// folds the provider/id label so typing any part of the provider or the model
// id narrows to this row, subsuming the manual id-prefix / label-substring /
// subsequence tiers the old visibleModels walked by hand.
func (i modelItem) FilterValue() string { return i.label() }

// Title is the row's primary line: a filled dot for the active model (an
// aligning blank otherwise) followed by the provider/id label.
func (i modelItem) Title() string {
	return activeMarker(i.active) + i.label()
}

// Description is the row's muted secondary line. It reports the model's context
// window in thousands of tokens when known (the "Nk ctx" the old picker showed),
// and falls back to the provider name alone so the line is never empty.
func (i modelItem) Description() string {
	if i.model.ContextWindow > 0 {
		return fmt.Sprintf("%dk ctx · %s", i.model.ContextWindow/1000, i.model.Provider)
	}
	return i.model.Provider
}

// sessionItem adapts a stored session to a list row. now is captured when the
// picker opens so every row's "updated" age is relative to a single instant,
// and current marks the session the user is already in so restoring it reads as
// a no-op rather than a switch.
type sessionItem struct {
	session session.Session
	now     time.Time
	current bool
}

// title returns the session's display title, standing in a placeholder for an
// untitled session so the row is never blank.
func (i sessionItem) title() string {
	if i.session.Title == "" {
		return "(untitled)"
	}
	return i.session.Title
}

// FilterValue matches the live filter against the session title plus its short
// id, so typing either the start of a session's name or a fragment of its id
// surfaces it — the haystack the old visibleSessions filtered by hand.
func (i sessionItem) FilterValue() string {
	return i.title() + " " + shortSessionID(i.session.ID)
}

// Title is the row's primary line: the session title, suffixed with a muted
// "(current)" tag when it is the active session.
func (i sessionItem) Title() string {
	t := i.title()
	if i.current {
		t += " " + styles.Muted.Render("(current)")
	}
	return t
}

// Description is the row's muted secondary line: message count, relative age,
// and short id — the metadata the old picker trailed after the title on one row,
// moved to the delegate's description line so the title stays uncluttered.
func (i sessionItem) Description() string {
	return fmt.Sprintf("%d msgs · %s · %s",
		i.session.MessageCount,
		relativeTime(i.session.UpdatedAt, i.now),
		shortSessionID(i.session.ID))
}

// ---------------------------------------------------------------------------
// List construction
// ---------------------------------------------------------------------------

// pickerDelegate returns the list item delegate the pickers share: the default
// two-line delegate restyled into the restrained palette. The stock delegate
// hardcodes a magenta selection; this recolors the selected row's left bar and
// text to the amber accent, dims the normal/description text to muted, and
// accents filter matches, so a picker matches the rest of the redesigned chat
// surface. Colors are pulled from the styles primitives (never hardcoded here)
// so the hex literals stay confined to the styles package.
func pickerDelegate() list.DefaultDelegate {
	d := list.NewDefaultDelegate()

	accent := styles.Accent.GetForeground()
	primary := styles.Primary.GetForeground()
	muted := styles.Muted.GetForeground()
	faint := styles.Faint.GetForeground()

	s := list.NewDefaultItemStyles(styles.IsDarkBackground())
	s.NormalTitle = s.NormalTitle.Foreground(primary)
	s.NormalDesc = s.NormalDesc.Foreground(muted)
	// The selected row keeps the delegate's left-border accent bar but in amber,
	// with amber title text and a muted description, so the cursor row reads as
	// active without the stock magenta.
	s.SelectedTitle = s.SelectedTitle.Foreground(accent).BorderForeground(accent)
	s.SelectedDesc = s.SelectedDesc.Foreground(muted).BorderForeground(accent)
	s.DimmedTitle = s.DimmedTitle.Foreground(muted)
	s.DimmedDesc = s.DimmedDesc.Foreground(faint)
	// Underline the runes that matched the live filter in the accent color, so a
	// reader sees why a row surfaced — the matched-rune emphasis the old picker
	// drew with highlightSessionMatch.
	s.FilterMatch = s.FilterMatch.Foreground(accent)
	d.Styles = s
	return d
}

// configurePicker applies the chrome shared by both pickers to a freshly built
// list: it strips the title bar (the modal frame titles the dialog), the status
// bar, and the help footer (the modal draws its own hint), keeps the paginator
// for long lists, and leaves incremental filtering on so "type to filter" works
// without the hand-rolled query state. The singular/plural item noun tunes the
// filter prompt's "N items" wording.
func configurePicker(l *list.Model, singular, plural string) {
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	l.SetShowPagination(true)
	l.SetFilteringEnabled(true)
	l.SetStatusBarItemName(singular, plural)
}

// newModelPicker builds the list model for the /model picker from the
// configured models, marking the row matching active (the session's current
// model id or "provider/id" label) and pre-selecting it so the picker opens on
// the row the user is using. The returned list is fully configured; the caller
// resizes it on WindowSizeMsg via setSize and reads the choice with
// selectedModel.
func newModelPicker(models []config.Model, active string) list.Model {
	items := make([]list.Item, 0, len(models))
	selected := 0
	for idx, mod := range models {
		label := mod.Provider + "/" + mod.ID
		isActive := active == mod.ID || active == label
		if isActive {
			selected = idx
		}
		items = append(items, modelItem{model: mod, active: isActive})
	}
	l := list.New(items, pickerDelegate(), pickerListWidth, pickerListHeight)
	configurePicker(&l, "model", "models")
	l.Select(selected)
	return l
}

// newSessionPicker builds the list model for the /sessions picker from the
// listed sessions, flagging the active session (when currentID is persisted) so
// its row shows "(current)". now is the instant every row's age renders against.
// The list opens on the first (most recent) row, matching the old picker, and
// marks rather than pre-selects the current session since the user is usually
// switching away from it.
func newSessionPicker(sessions []session.Session, currentID string, persisted bool, now time.Time) list.Model {
	items := make([]list.Item, 0, len(sessions))
	for _, s := range sessions {
		items = append(items, sessionItem{
			session: s,
			now:     now,
			current: persisted && s.ID == currentID,
		})
	}
	l := list.New(items, pickerDelegate(), pickerListWidth, pickerListHeight)
	configurePicker(&l, "session", "sessions")
	return l
}

// ---------------------------------------------------------------------------
// Selection read-back
// ---------------------------------------------------------------------------

// selectedModel returns the model under the picker's cursor and whether a row
// was selected. It is false when the (filtered) list is empty, so the caller
// keeps the picker open rather than applying an arbitrary model — the guard the
// old enter handler made explicitly.
func selectedModel(l list.Model) (config.Model, bool) {
	it, ok := l.SelectedItem().(modelItem)
	if !ok {
		return config.Model{}, false
	}
	return it.model, true
}

// selectedSession returns the session under the picker's cursor and whether a
// row was selected. Like selectedModel it is false on an empty filtered list so
// the caller does not restore an arbitrary session.
func selectedSession(l list.Model) (session.Session, bool) {
	it, ok := l.SelectedItem().(sessionItem)
	if !ok {
		return session.Session{}, false
	}
	return it.session, true
}

// ---------------------------------------------------------------------------
// Modal rendering
// ---------------------------------------------------------------------------

// pickerModal frames a picker's list as a centered modal. It titles the box,
// embeds the list's own view (rows, paginator, and — while filtering — the
// filter prompt), appends a one-line key hint, and centers the whole frame in
// the terminal with lipgloss.Place so the dialog floats over the transcript.
// width and height are the terminal size; a non-positive size falls back to the
// list's own measured extent so the modal still renders before the first
// WindowSizeMsg. When filtering, esc clears the filter rather than closing, so
// the hint reflects that.
func pickerModal(l list.Model, title string, width, height int) string {
	hint := "type to filter · ↑/↓ move · enter select · esc cancel"
	if l.SettingFilter() {
		hint = "type to filter · enter accept · esc clear"
	}

	body := lipgloss.JoinVertical(
		lipgloss.Left,
		styles.Accent.Render(title),
		"",
		l.View(),
		"",
		styles.Faint.Render(hint),
	)
	box := styles.ModalBox.Render(body)

	if width <= 0 {
		width = lipgloss.Width(box)
	}
	if height <= 0 {
		height = lipgloss.Height(box)
	}
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

// pickerListSize derives the inner list width and height from the terminal size,
// reserving rows and columns for the modal border, padding, title, hint, and the
// list's filter line so the framed modal fits a terminal of the given size. The
// caller hands these to list.SetSize on every WindowSizeMsg. Both dimensions are
// floored so the list never receives a non-positive size (which would blank its
// view); they are also capped at the picker's default extent so a tall terminal
// does not stretch a short list into a wall of blank rows.
func pickerListSize(width, height int) (w, h int) {
	// Horizontal: 2 border + 4 padding (ModalBox pads 0,2 → 2 each side, drawn
	// inside the 1-cell border).
	w = width - 8
	if w < 1 {
		w = pickerListWidth
	}
	// Vertical: 2 border + 2 padding (ModalBox pads 1 top/bottom) + 1 title +
	// 1 blank + 1 blank + 1 hint + 1 filter line the list reserves = 9 rows of
	// chrome around the list body.
	h = height - 11
	if h < 1 {
		h = pickerListHeight
	}
	if h > pickerListHeight {
		h = pickerListHeight
	}
	return w, h
}

// ---------------------------------------------------------------------------
// Streaming spinner
// ---------------------------------------------------------------------------

// newStreamSpinner builds the spinner shown while the agent is producing a turn.
// It uses the MiniDot braille set — the same glyphs the old hand-cycled
// spinnerFrames used — but driven by the spinner's own ~12fps tick so it
// animates smoothly rather than advancing once per one-second status tick. It is
// tinted amber (the in-progress accent) so an active turn reads at a glance.
//
// Compose wiring (kept here so the spinner is self-contained):
//   - store the model: m.spinner = newStreamSpinner()
//   - start it when a turn begins: return spinner ticking by batching
//     m.spinner.Tick alongside the run command (in the submit/agentrun path),
//     so the first spinner.TickMsg arrives and the animation loop starts.
//   - in Update, handle spinner.TickMsg: forward it to m.spinner.Update only
//     while m.running, and return the resulting cmd; once the turn finishes
//     (m.running is false) stop re-forwarding so the ticking loop ends and the
//     spinner goes idle without a dangling timer.
//   - render m.spinner.View() in the running status segment while m.running.
func newStreamSpinner() spinner.Model {
	return spinner.New(
		spinner.WithSpinner(spinner.MiniDot),
		spinner.WithStyle(styles.Accent),
	)
}

// stepStreamSpinner advances the streaming spinner from a spinner.TickMsg and
// returns the command that schedules its next frame — but only while a turn is
// running. Once running is false it returns the spinner unchanged with a nil
// command, so the per-frame tick loop terminates cleanly at turn end instead of
// spinning a timer forever. Compose calls this from its spinner.TickMsg case.
func stepStreamSpinner(sp spinner.Model, msg spinner.TickMsg, running bool) (spinner.Model, tea.Cmd) {
	if !running {
		return sp, nil
	}
	return sp.Update(msg)
}

// updateList forwards msg to the bubbles list's Update method and returns the
// updated list.Model and tea.Cmd. bubbles/v2 list.Model.Update already returns
// (list.Model, tea.Cmd) — not (tea.Model, tea.Cmd) — so no type assertion is
// needed; this wrapper exists to give callers a uniform call site and to make
// the forwarding explicit in the picker key handlers.
func updateList(l list.Model, msg tea.Msg) (list.Model, tea.Cmd) {
	return l.Update(msg)
}
