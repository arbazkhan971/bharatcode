package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/agent"
	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/filetracker"
	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/recipe"
	"github.com/arbazkhan971/bharatcode/internal/session"
	"github.com/arbazkhan971/bharatcode/internal/tui/dialog"
	"github.com/arbazkhan971/bharatcode/internal/tui/diff"
	tea "github.com/charmbracelet/bubbletea/v2"
)

// recentSessionLimit bounds the number of sessions shown in the /sessions
// picker so the dialog stays readable.
const recentSessionLimit = 20

// sessionWindow caps how many session rows the picker draws at once. A full
// recentSessionLimit-long list (plus the filter line and the keybinding hint)
// can run taller than a short terminal, and the dialog clamps width but not
// height, so without a cap the cursor could scroll off the top of the screen
// with no way to see the selected row. When more rows match than fit, the
// picker scrolls a window of this many rows that follows the cursor and stands
// the hidden rows in with a muted "⋯ N more above/below" marker — the way the
// diff viewer folds long context and the completion menus report overflow. It
// is smaller than recentSessionLimit so the windowing actually engages on a
// long list.
const sessionWindow = 10

// sessionWindowBounds returns the half-open [start, end) range of session rows
// the picker shows for a list of total rows with the cursor at cursor, scrolling
// a window of at most sessionWindow rows so the selected row stays visible. The
// window is centered on the cursor where possible and clamped to either end, so
// the first and last rows are reachable without a half-empty window, and an
// out-of-range cursor still yields a valid in-bounds window. When the whole list
// fits within the window it is shown entire ([0,total)), leaving the short-list
// case byte-for-byte unchanged.
func sessionWindowBounds(cursor, total int) (start, end int) {
	if total <= sessionWindow {
		return 0, total
	}
	start = cursor - sessionWindow/2
	if start < 0 {
		start = 0
	}
	end = start + sessionWindow
	if end > total {
		end = total
		start = end - sessionWindow
	}
	return start, end
}

// openSessionPicker loads recent sessions and pushes a selectable picker. When
// no sessions exist it surfaces an informational dialog instead.
func (m *model) openSessionPicker() (tea.Model, tea.Cmd) {
	sessions, err := m.deps.Sessions.List(m.ctx, session.ListFilter{Limit: recentSessionLimit})
	if err != nil {
		m.dialogs.Push(&dialog.Text{DialogID: "sessions", Title: "Sessions", Body: "Could not list sessions: " + err.Error(), Theme: m.theme})
		return m, nil
	}
	if len(sessions) == 0 {
		m.dialogs.Push(&dialog.Text{DialogID: "sessions", Title: "Sessions", Body: "No saved sessions yet.", Theme: m.theme})
		return m, nil
	}
	m.sessionCandidates = sessions
	m.sessionCursor = 0
	m.sessionFilter = ""
	m.dialogs.Push(&dialog.Text{
		DialogID: "sessions",
		Title:    "Sessions",
		Body:     m.sessionPickerBody(),
		Theme:    m.theme,
	})
	return m, nil
}

// visibleSessions returns the picker rows that match the live filter query.
// An empty query returns every candidate in candidate (recency) order. A query
// is matched case-insensitively against the session title and short id in three
// ranked bands: rows whose title begins with the query rank first, then rows
// whose title-and-id haystack contains the query as a substring, then rows it
// matches only as a scattered subsequence (so "psr" finds "Parser refactor").
// Leading the title-prefix band ahead of a mid-string substring mirrors the
// @-file picker's base-name-prefix tier, so typing the start of a session's name
// surfaces it first rather than burying it under an older session that merely
// contains those letters. Within each band the original recency order is
// preserved, matching the fuzzy session switchers in Claude Code and opencode.
func (m *model) visibleSessions() []session.Session {
	if m.sessionFilter == "" {
		return m.sessionCandidates
	}
	q := strings.ToLower(m.sessionFilter)
	var prefix, substr, subseq []session.Session
	for _, s := range m.sessionCandidates {
		title := strings.ToLower(s.Title)
		hay := title + " " + strings.ToLower(shortSessionID(s.ID))
		switch {
		case strings.HasPrefix(title, q):
			prefix = append(prefix, s)
		case strings.Contains(hay, q):
			substr = append(substr, s)
		case isSubsequence(q, hay):
			subseq = append(subseq, s)
		}
	}
	out := make([]session.Session, 0, len(prefix)+len(substr)+len(subseq))
	out = append(out, prefix...)
	out = append(out, substr...)
	return append(out, subseq...)
}

// sessionPickerBody renders the session list with a cursor marker and a hint.
// When a filter query is active it is echoed above the list, and an empty
// result set is reported so the user knows the query matched nothing.
func (m *model) sessionPickerBody() string {
	visible := m.visibleSessions()
	lines := make([]string, 0, len(visible)+4)
	if m.sessionFilter != "" {
		// Echo the filter with a "N of M" tally so the user can see how far the
		// query has narrowed the list at a glance, the way the completion menus
		// report their match counts rather than leaving the reader to count rows.
		count := m.theme.Muted.Render(fmt.Sprintf("· %d of %d", len(visible), len(m.sessionCandidates)))
		lines = append(lines, "Filter: "+m.sessionFilter+" "+count, "")
	}
	if len(visible) == 0 {
		lines = append(lines, "(no sessions match)")
	}
	// Draw only the window of rows around the cursor so a long list never
	// overflows the modal; the rows scrolled out of view are stood in for by a
	// muted "⋯ N more" marker on whichever side they fell.
	start, end := sessionWindowBounds(m.sessionCursor, len(visible))
	if start > 0 {
		lines = append(lines, m.theme.Muted.Render(fmt.Sprintf("⋯ %d more above", start)))
	}
	for i := start; i < end; i++ {
		s := visible[i]
		marker := "  "
		if i == m.sessionCursor {
			marker = "> "
		}
		title := s.Title
		if title == "" {
			title = "(untitled)"
		}
		display := title
		if m.sessionFilter != "" {
			display = m.highlightSessionMatch(title, m.sessionFilter)
		}
		row := fmt.Sprintf("%s%s · %d msgs · %s · %s", marker, display, s.MessageCount, relativeTime(s.UpdatedAt, time.Now()), shortSessionID(s.ID))
		// Flag the session the user is already in so restoring it reads as a no-op
		// rather than a switch, the way Claude Code and opencode mark the active
		// session in their switcher.
		if m.sessionPersisted && s.ID == m.sessionID {
			row += " · " + m.theme.Muted.Render("(current)")
		}
		lines = append(lines, row)
	}
	if end < len(visible) {
		lines = append(lines, m.theme.Muted.Render(fmt.Sprintf("⋯ %d more below", len(visible)-end)))
	}
	lines = append(lines, "", "type to fuzzy filter · ↑/↓ to move · pgup/pgdn by page · home/end to jump · enter to restore · esc to cancel")
	return strings.Join(lines, "\n")
}

// highlightSessionMatch accents the runes of title that matched the active
// filter query, leaving the rest in the default color so the title stays
// readable beside the muted metadata. It mirrors the matched-rune emphasis the
// @-file and slash-command menus draw via matchPositions, so a reader can see
// why a session surfaced under a fuzzy filter rather than scanning the row to
// guess. The query is matched against the title alone; a query that connected
// only through a session's id (the substring/subsequence bands in
// visibleSessions match the title-plus-id haystack) leaves the title unstyled,
// since there are no title runes to emphasize. An empty query, or a title with
// no match, returns the title unchanged so the styling is added only where it
// explains the result.
func (m *model) highlightSessionMatch(title, query string) string {
	pos := matchPositions(query, title)
	if len(pos) == 0 {
		return title
	}
	hit := make(map[int]bool, len(pos))
	for _, p := range pos {
		hit[p] = true
	}
	runes := []rune(title)
	var b strings.Builder
	for i := 0; i < len(runes); {
		on := hit[i]
		j := i
		for j < len(runes) && hit[j] == on {
			j++
		}
		seg := string(runes[i:j])
		if on {
			b.WriteString(m.theme.Accent.Render(seg))
		} else {
			b.WriteString(seg)
		}
		i = j
	}
	return b.String()
}

// handleSessionPickerKey processes navigation and selection while the session
// picker is open. It returns whether the key was consumed; an unconsumed key
// (other than enter/esc) falls through to the dialog's own handler.
func (m *model) handleSessionPickerKey(msg tea.KeyPressMsg) (consumed bool, cmd tea.Cmd) {
	switch msg.String() {
	case "up":
		if m.sessionCursor > 0 {
			m.sessionCursor--
			m.refreshSessionPicker()
		}
		return true, nil
	case "down":
		if m.sessionCursor < len(m.visibleSessions())-1 {
			m.sessionCursor++
			m.refreshSessionPicker()
		}
		return true, nil
	case "home":
		// Jump to the first visible row, mirroring the chat's Home binding
		// (oldest message). A no-op when already at the top.
		if m.sessionCursor != 0 {
			m.sessionCursor = 0
			m.refreshSessionPicker()
		}
		return true, nil
	case "end":
		// Jump to the last visible row, mirroring the chat's End binding (newest
		// message). A no-op on an empty or single-row list.
		if last := len(m.visibleSessions()) - 1; last >= 0 && m.sessionCursor != last {
			m.sessionCursor = last
			m.refreshSessionPicker()
		}
		return true, nil
	case "pgup":
		// Page up moves the cursor a windowful at a time, mirroring the chat's
		// PgUp, so a long session list is traversable faster than one row per
		// keystroke. The step is clamped at the first row.
		if m.sessionCursor > 0 {
			m.sessionCursor -= sessionWindow
			if m.sessionCursor < 0 {
				m.sessionCursor = 0
			}
			m.refreshSessionPicker()
		}
		return true, nil
	case "pgdown":
		// Page down is the mirror of PgUp, advancing the cursor a windowful and
		// clamping at the last visible row.
		if last := len(m.visibleSessions()) - 1; last >= 0 && m.sessionCursor < last {
			m.sessionCursor += sessionWindow
			if m.sessionCursor > last {
				m.sessionCursor = last
			}
			m.refreshSessionPicker()
		}
		return true, nil
	case "backspace":
		// Backspace edits the live filter rather than dismissing the picker.
		if m.sessionFilter != "" {
			r := []rune(m.sessionFilter)
			m.sessionFilter = string(r[:len(r)-1])
			m.sessionCursor = 0
			m.refreshSessionPicker()
		}
		return true, nil
	case "enter":
		visible := m.visibleSessions()
		if len(visible) == 0 {
			// Nothing matches the filter; keep the picker open rather than
			// restoring an arbitrary session.
			return true, nil
		}
		chosen := visible[m.sessionCursor]
		m.dialogs.Pop()
		m.sessionCandidates = nil
		m.sessionFilter = ""
		return true, m.restoreSession(chosen.ID)
	default:
		// A printable keypress extends the filter query. Anything else (esc,
		// etc.) falls through to the dialog's own handler.
		if text := msg.Key().Text; text != "" {
			m.sessionFilter += text
			m.sessionCursor = 0
			m.refreshSessionPicker()
			return true, nil
		}
		return false, nil
	}
}

// refreshSessionPicker re-renders the open picker dialog so the moved cursor is
// reflected. It replaces the top dialog in place.
func (m *model) refreshSessionPicker() {
	m.dialogs.Pop()
	m.dialogs.Push(&dialog.Text{
		DialogID: "sessions",
		Title:    "Sessions",
		Body:     m.sessionPickerBody(),
		Theme:    m.theme,
	})
}

// modelWindow caps how many model rows the interactive picker draws at once.
// The model list is typically short, but a user with many configured models
// would otherwise get a dialog taller than the terminal; the window scrolls to
// follow the cursor exactly as the session picker's sessionWindow does.
const modelWindow = 10

// modelWindowBounds mirrors sessionWindowBounds but for the model picker list.
func modelWindowBounds(cursor, total int) (start, end int) {
	if total <= modelWindow {
		return 0, total
	}
	start = cursor - modelWindow/2
	if start < 0 {
		start = 0
	}
	end = start + modelWindow
	if end > total {
		end = total
		start = end - modelWindow
	}
	return start, end
}

// visibleModels returns the picker rows that match the live filter query.
// An empty query returns every candidate in config order. A non-empty query is
// matched case-insensitively against the model ID and provider name in three
// ranked tiers: rows whose ID begins with the query come first, then rows
// whose "provider/ID" label contains the query as a substring, then rows it
// matches only as a scattered subsequence — the same three-tier ranking the
// session and @-file pickers use.
func (m *model) visibleModels() []config.Model {
	if m.modelFilter == "" {
		return m.modelCandidates
	}
	q := strings.ToLower(m.modelFilter)
	var prefix, substr, subseq []config.Model
	for _, mod := range m.modelCandidates {
		id := strings.ToLower(mod.ID)
		hay := strings.ToLower(mod.Provider) + "/" + id
		switch {
		case strings.HasPrefix(id, q):
			prefix = append(prefix, mod)
		case strings.Contains(hay, q):
			substr = append(substr, mod)
		case isSubsequence(q, hay):
			subseq = append(subseq, mod)
		}
	}
	out := make([]config.Model, 0, len(prefix)+len(substr)+len(subseq))
	out = append(out, prefix...)
	out = append(out, substr...)
	return append(out, subseq...)
}

// modelPickerBody renders the interactive model list with a cursor marker, an
// optional filter echo, and a keybinding hint footer. It mirrors the session
// picker's layout so both pickers feel like the same pattern.
func (m *model) modelPickerBody() string {
	visible := m.visibleModels()
	lines := make([]string, 0, len(visible)+4)
	if m.modelFilter != "" {
		count := m.theme.Muted.Render(fmt.Sprintf("· %d of %d", len(visible), len(m.modelCandidates)))
		lines = append(lines, "Filter: "+m.modelFilter+" "+count, "")
	}
	if len(visible) == 0 {
		lines = append(lines, "(no models match)")
	}
	start, end := modelWindowBounds(m.modelCursor, len(visible))
	if start > 0 {
		lines = append(lines, m.theme.Muted.Render(fmt.Sprintf("⋯ %d more above", start)))
	}
	for i := start; i < end; i++ {
		mod := visible[i]
		label := mod.Provider + "/" + mod.ID
		active := m.status.Model == mod.ID || m.status.Model == label
		marker := "  "
		if i == m.modelCursor {
			marker = "> "
		}
		row := marker + activeMarker(active) + label
		if mod.ContextWindow > 0 {
			row += m.theme.Muted.Render(fmt.Sprintf("  %dk ctx", mod.ContextWindow/1000))
		}
		lines = append(lines, row)
	}
	if end < len(visible) {
		lines = append(lines, m.theme.Muted.Render(fmt.Sprintf("⋯ %d more below", len(visible)-end)))
	}
	lines = append(lines, "", "type to filter · ↑/↓ to move · enter to select · esc to cancel")
	return strings.Join(lines, "\n")
}

// handleModelPickerKey processes navigation and selection while the model
// picker is open. It mirrors handleSessionPickerKey exactly, returning whether
// the key was consumed so an unconsumed key falls through to the dialog's own
// handler (esc to dismiss).
func (m *model) handleModelPickerKey(msg tea.KeyPressMsg) (consumed bool, cmd tea.Cmd) {
	switch msg.String() {
	case "up":
		if m.modelCursor > 0 {
			m.modelCursor--
			m.refreshModelPicker()
		}
		return true, nil
	case "down":
		if m.modelCursor < len(m.visibleModels())-1 {
			m.modelCursor++
			m.refreshModelPicker()
		}
		return true, nil
	case "home":
		if m.modelCursor != 0 {
			m.modelCursor = 0
			m.refreshModelPicker()
		}
		return true, nil
	case "end":
		if last := len(m.visibleModels()) - 1; last >= 0 && m.modelCursor != last {
			m.modelCursor = last
			m.refreshModelPicker()
		}
		return true, nil
	case "pgup":
		if m.modelCursor > 0 {
			m.modelCursor -= modelWindow
			if m.modelCursor < 0 {
				m.modelCursor = 0
			}
			m.refreshModelPicker()
		}
		return true, nil
	case "pgdown":
		if last := len(m.visibleModels()) - 1; last >= 0 && m.modelCursor < last {
			m.modelCursor += modelWindow
			if m.modelCursor > last {
				m.modelCursor = last
			}
			m.refreshModelPicker()
		}
		return true, nil
	case "backspace":
		if m.modelFilter != "" {
			r := []rune(m.modelFilter)
			m.modelFilter = string(r[:len(r)-1])
			m.modelCursor = 0
			m.refreshModelPicker()
		}
		return true, nil
	case "enter":
		visible := m.visibleModels()
		if len(visible) == 0 {
			return true, nil
		}
		chosen := visible[m.modelCursor]
		m.dialogs.Pop()
		m.modelCandidates = nil
		m.modelFilter = ""
		m.applyModel(chosen)
		return true, nil
	default:
		if text := msg.Key().Text; text != "" {
			m.modelFilter += text
			m.modelCursor = 0
			m.refreshModelPicker()
			return true, nil
		}
		return false, nil
	}
}

// refreshModelPicker re-renders the open model picker dialog so the moved
// cursor or updated filter is reflected. It replaces the top dialog in place,
// mirroring refreshSessionPicker.
func (m *model) refreshModelPicker() {
	m.dialogs.Pop()
	m.dialogs.Push(&dialog.Text{
		DialogID: "model_picker",
		Title:    "Models",
		Body:     m.modelPickerBody(),
		Theme:    m.theme,
	})
}

// applyModel updates the active model display to the selected model. The
// status bar model field is updated immediately so the UI reflects the choice
// on the next render. This mirrors how the session picker updates status.Model
// when restoring a session.
func (m *model) applyModel(mod config.Model) {
	m.status.Model = mod.ID
}

// restoreSession switches the active session to id and loads its persisted
// transcript into the chat view. It updates the session identity shown in the
// status bar and footer and refreshes the ledger summary for the new session.
func (m *model) restoreSession(id string) tea.Cmd {
	sess, err := m.deps.Sessions.Get(m.ctx, id)
	if err != nil {
		m.dialogs.Push(&dialog.Text{DialogID: "error", Title: "Restore failed", Body: err.Error(), Theme: m.theme})
		return nil
	}
	msgs, err := m.deps.Sessions.Messages(m.ctx, id)
	if err != nil {
		m.dialogs.Push(&dialog.Text{DialogID: "error", Title: "Restore failed", Body: err.Error(), Theme: m.theme})
		return nil
	}

	m.sessionID = sess.ID
	m.sessionPersisted = true
	m.status.SessionID = sess.ID
	m.status.Model = sess.Model
	m.status.Agent = sess.Agent
	m.footer.SessionID = sess.ID
	// Reset the session-scoped spend; the ledger bus repopulates it for the
	// restored session as fresh summaries arrive.
	m.footer.CostINR = 0

	m.chat.Clear()
	for _, msg := range msgs {
		m.chat.Append(msg)
	}
	// Refresh the ledger footer for the newly active session. The summary
	// command is best-effort and returns nil on error, so a quiet or
	// unavailable ledger never blocks the switch; live ledger-bus events keep
	// the footer current thereafter.
	return m.waitLedger()
}

// handleRevert undoes the file changes the active session made, restoring each
// file to the state it had before the session began via the file tracker's
// content snapshots. Because reverting overwrites or deletes files on disk, a
// bare "/revert" performs a dry run and lists what would change, asking the
// user to confirm with "/revert apply"; "/revert force" additionally reverts
// files that were modified out of band since the session last wrote them. This
// brings opencode's /undo and Claude Code's rewind to the TUI — the underlying
// RevertSession already backs the `bharatcode revert` CLI command — while
// keeping the destructive step behind an explicit confirmation.
func (m *model) handleRevert(text string) (tea.Model, tea.Cmd) {
	_, args := splitSlash(text)
	arg := strings.ToLower(strings.TrimSpace(args))

	if !m.sessionPersisted {
		m.dialogs.Push(&dialog.Text{DialogID: "revert", Title: "Revert", Body: "No active session to revert yet. Send a prompt first.", Theme: m.theme})
		return m, nil
	}

	var apply, force bool
	switch arg {
	case "":
		// A bare /revert is a dry run: show what would change first.
	case "apply":
		apply = true
	case "force":
		apply, force = true, true
	default:
		m.dialogs.Push(&dialog.Text{DialogID: "revert", Title: "Revert", Body: "Usage: /revert [apply|force]\n\n/revert previews the changes, /revert apply undoes them, /revert force also reverts files modified outside the session.", Theme: m.theme})
		return m, nil
	}

	outcomes, err := m.deps.FileTracker.RevertSession(m.ctx, m.sessionID, filetracker.RevertOptions{
		Force:  force,
		DryRun: !apply,
	})
	if err != nil {
		m.dialogs.Push(&dialog.Text{DialogID: "error", Title: "Revert failed", Body: err.Error(), Theme: m.theme})
		return m, nil
	}

	m.dialogs.Push(&dialog.Text{DialogID: "revert", Title: "Revert", Body: revertSummary(outcomes, apply), Theme: m.theme})
	return m, nil
}

// revertSummary renders the outcome of a /revert run. For a dry run it lists
// what would change and how to confirm; for an applied run it reports what was
// restored, deleted, or skipped. Each row leads with the action so the columns
// align, mirroring the table the CLI prints.
func revertSummary(outcomes []filetracker.RevertOutcome, applied bool) string {
	if len(outcomes) == 0 {
		return "This session changed no files."
	}
	var changed, skipped int
	lines := make([]string, 0, len(outcomes))
	for _, o := range outcomes {
		if o.Action == filetracker.RevertSkipped {
			skipped++
		} else {
			changed++
		}
		line := fmt.Sprintf("%-9s %s", string(o.Action), o.Path)
		if o.Reason != "" {
			line += " — " + o.Reason
		}
		lines = append(lines, line)
	}

	var header string
	if applied {
		header = fmt.Sprintf("Reverted %d file(s), %d skipped.", changed, skipped)
	} else {
		header = fmt.Sprintf("Dry run — %d file(s) would be reverted, %d skipped.", changed, skipped)
	}
	body := header + "\n\n" + strings.Join(lines, "\n")
	if !applied {
		body += "\n\nRun /revert apply to undo these changes."
		if skipped > 0 {
			body += "\n/revert force also reverts files changed outside the session."
		}
	}
	return body
}

// handleFork branches the current session and switches to the new fork,
// surfacing a confirmation dialog. It is a no-op with an explanatory dialog
// when there is no persisted session to fork.
func (m *model) handleFork() (tea.Model, tea.Cmd) {
	if !m.sessionPersisted {
		m.dialogs.Push(&dialog.Text{DialogID: "fork", Title: "Fork", Body: "No active session to fork yet. Send a prompt first.", Theme: m.theme})
		return m, nil
	}
	forked, err := forkSession(m.ctx, m.deps.Sessions, m.sessionID)
	if err != nil {
		m.dialogs.Push(&dialog.Text{DialogID: "error", Title: "Fork failed", Body: err.Error(), Theme: m.theme})
		return m, nil
	}
	cmd := m.restoreSession(forked.ID)
	m.dialogs.Push(&dialog.Text{
		DialogID: "fork",
		Title:    "Forked session",
		Body:     fmt.Sprintf("Branched into %s\nNow editing %s", forked.Title, shortSessionID(forked.ID)),
		Theme:    m.theme,
	})
	return m, cmd
}

// compactStreamID is the chat-list key for the /compact confirmation. A fixed
// id keeps it distinct from per-turn assistant bubbles; only one confirmation
// is shown at a time, so it does not need a counter suffix.
const compactStreamID = "local-compact"

// compactConfirmation is the message surfaced in the chat after a successful
// manual context compaction.
const compactConfirmation = "Context compacted — older turns summarized."

// handleCompact condenses the active session's conversation in memory via the
// agent loop's Compactor seam, so the next provider request sends a smaller
// history. It is a no-op with an explanatory dialog when there is no persisted
// session yet. On success it surfaces a confirmation in the chat. Compaction
// never mutates the on-disk transcript; it only changes what the agent sends to
// the provider on subsequent turns.
func (m *model) handleCompact() (tea.Model, tea.Cmd) {
	if !m.sessionPersisted {
		m.dialogs.Push(&dialog.Text{DialogID: "compact", Title: "Compact", Body: "No active session to compact yet. Send a prompt first.", Theme: m.theme})
		return m, nil
	}
	if err := m.deps.Agent.Compact(m.ctx, m.sessionID); err != nil {
		m.dialogs.Push(&dialog.Text{DialogID: "error", Title: "Compact failed", Body: err.Error(), Theme: m.theme})
		return m, nil
	}
	m.chat.Stream(compactStreamID, compactConfirmation)
	m.chat.FinishStream(compactStreamID)
	m.chat.Reindex(compactStreamID)
	return m, nil
}

// handleDiff renders the most recent edit, multiedit, or write tool call for
// the active session as a before/after unified diff. It surfaces an
// informational dialog when no such change exists.
func (m *model) handleDiff() (tea.Model, tea.Cmd) {
	if !m.sessionPersisted {
		m.dialogs.Push(&dialog.Text{DialogID: "diff", Title: "Diff", Body: "No edit diff is available yet.", Theme: m.theme})
		return m, nil
	}
	msgs, err := m.deps.Sessions.Messages(m.ctx, m.sessionID)
	if err != nil {
		m.dialogs.Push(&dialog.Text{DialogID: "diff", Title: "Diff", Body: "Could not load messages: " + err.Error(), Theme: m.theme})
		return m, nil
	}
	diffs := latestEditDiffs(msgs)
	if len(diffs) == 0 {
		m.dialogs.Push(&dialog.Text{DialogID: "diff", Title: "Diff", Body: "No edit diff is available yet.", Theme: m.theme})
		return m, nil
	}
	patch := unifiedPatch(diffs)
	viewer := diff.New(m.theme)
	rendered := viewer.RenderUnifiedNumbered(patch, max(1, m.width-6))
	// Lead with a diffstat summary so the scope of the change is visible before
	// scrolling, matching the header Claude Code and opencode show above a diff.
	// A multi-file change gets a per-file breakdown beneath the aggregate line.
	if stat := viewer.StatLines(patch, max(1, m.width-6)); stat != "" {
		rendered = stat + "\n\n" + rendered
	}
	// Capture the raw patch in a closure so 'y' in the dialog copies it to the
	// clipboard without requiring any extra model state.
	rawPatch := patch
	copyFn := systemClipboardCopy
	if m.copyToClipboard != nil {
		copyFn = m.copyToClipboard
	}
	m.dialogs.Push(&dialog.ScrollableText{
		DialogID: "diff",
		Title:    "Diff",
		Body:     rendered,
		Theme:    m.theme,
		Height:   m.height,
		CopyFn:   func() error { return copyFn(rawPatch) },
	})
	return m, nil
}

// planEnabledBody and planDisabledBody are the dialog bodies shown when plan
// mode is toggled, kept as constants so the wording stays consistent.
const (
	planEnabledBody = "Plan mode on. The agent is restricted to read-only tools and will propose a plan instead of editing. Use /approve to execute."
	approveBody     = "Plan approved. Execution tools are enabled again; send your next prompt to proceed."
	approveNoopBody = "Not in plan mode. Nothing to approve."
)

// handlePlan turns on plan mode on the live agent loop so the next turn is
// restricted to read-only tools and the agent is prompted to produce a plan
// rather than execute. It takes effect on the next provider call; the existing
// session is preserved. /approve clears it.
func (m *model) handlePlan() (tea.Model, tea.Cmd) {
	if m.deps.Agent.PlanMode() {
		m.dialogs.Push(&dialog.Text{DialogID: "plan", Title: "Plan mode", Body: planEnabledBody, Theme: m.theme})
		return m, nil
	}
	m.deps.Agent.SetPlanMode(true)
	m.dialogs.Push(&dialog.Text{DialogID: "plan", Title: "Plan mode", Body: planEnabledBody, Theme: m.theme})
	return m, nil
}

// handleApprove exits plan mode on the live agent loop, re-enabling execution
// tools. It is a no-op (with an explanatory dialog) when the loop is not in plan
// mode. When a plan is stored, it shows the plan for final review before
// auto-continuing execution with the approved plan.
func (m *model) handleApprove() (tea.Model, tea.Cmd) {
	if !m.deps.Agent.PlanMode() {
		m.dialogs.Push(&dialog.Text{DialogID: "plan", Title: "Approve", Body: approveNoopBody, Theme: m.theme})
		return m, nil
	}

	// Retrieve the stored plan for display in the approval dialog.
	planText := m.deps.Coordinator.PlanFor(m.sessionID)

	// Show the plan in the approval dialog so the user can review it before execution.
	if planText != "" {
		m.dialogs.Push(&dialog.Text{DialogID: "plan", Title: "Approve Plan", Body: planText, Theme: m.theme})
	} else {
		m.dialogs.Push(&dialog.Text{DialogID: "plan", Title: "Approve", Body: approveBody, Theme: m.theme})
	}

	// Approve the plan: transitions the loop out of plan mode and retrieves the plan.
	planText = m.deps.Coordinator.ApprovePlan(m.sessionID, m.deps.Agent)

	// Auto-continue execution: seed the next turn with the approved plan.
	// Extract the prompt text from the seed message so continueRun can render it.
	seed := agent.SeedMessageFromPlan(m.sessionID, planText)
	var seedText string
	for _, block := range seed.Content {
		if tb, ok := block.(message.TextBlock); ok {
			seedText = tb.Text
			break
		}
	}
	return m, m.continueRun(seedText)
}

// handleStatus pushes a panel summarizing the active model, agent, session,
// message count, approval mode, and INR spend for this session.
func (m *model) handleStatus() (tea.Model, tea.Cmd) {
	m.dialogs.Push(&dialog.Text{DialogID: "status", Title: "Status", Body: m.statusPanel(), Theme: m.theme})
	return m, nil
}

// statusPanel renders the status panel body.
func (m *model) statusPanel() string {
	lines := []string{
		"Model: " + m.status.Model,
		"Agent: " + m.status.Agent,
		"Session: " + m.sessionID,
		fmt.Sprintf("Messages: %d", m.sessionMessageCount()),
		"Approval: " + approvalModeLabel(m.deps.Permission.GetApprovalMode()),
		fmt.Sprintf("Session spend: ₹%.2f", m.footer.CostINR),
	}
	if m.footer.MonthlyBudgetINR > 0 {
		lines = append(lines, fmt.Sprintf("Monthly budget: ₹%.2f", m.footer.MonthlyBudgetINR))
	}
	return strings.Join(lines, "\n")
}

// sessionMessageCount returns the persisted message count for the active
// session, or 0 when the session has not been persisted or cannot be read.
func (m *model) sessionMessageCount() int {
	if !m.sessionPersisted {
		return 0
	}
	sess, err := m.deps.Sessions.Get(m.ctx, m.sessionID)
	if err != nil {
		return 0
	}
	return sess.MessageCount
}

// handleRegistryPrompt looks up name in the prompt registry and, when found,
// renders it with the remaining args spliced into {{input}} and submits the
// expansion to the agent. It reports whether the command was handled as a
// registry prompt; an unregistered name returns false so the caller can fall
// back to the unknown-command dialog.
func (m *model) handleRegistryPrompt(name string, args string) (handled bool, model tea.Model, cmd tea.Cmd) {
	if m.deps.Prompts == nil {
		return false, m, nil
	}
	if _, ok := m.deps.Prompts.Get(name); !ok {
		return false, m, nil
	}
	rendered, err := m.deps.Prompts.RenderSlash(name, args)
	if err != nil {
		m.dialogs.Push(&dialog.Text{DialogID: "error", Title: "Prompt error", Body: err.Error(), Theme: m.theme})
		return true, m, nil
	}
	// Splice in any !`cmd` inline shell substitutions so the template can embed
	// live repository state (git status, branch, test output) at invocation
	// time, matching Claude Code / pi custom-command behaviour.
	rendered = expandBangCommands(rendered, m.runBangCommand)
	model, cmd = m.startRun(rendered)
	return true, model, cmd
}

// handleRegistryRecipe looks up name in the recipe registry and, when found,
// collects any RequirementUserPrompt parameters interactively (pushing one
// dialog per parameter), then renders the recipe and submits the result to the
// agent via m.startRun. When args is non-empty it is pre-populated as an
// "input" parameter value (mirroring handleRegistryPrompt's {{input}} binding)
// and also used as the fallback value for any single required user_prompt
// parameter. It reports handled=true when the name matches a recipe; an
// unregistered name returns handled=false so the caller can fall back to the
// unknown-command dialog.
func (m *model) handleRegistryRecipe(name string, args string) (handled bool, mod tea.Model, cmd tea.Cmd) {
	if m.deps.Recipes == nil {
		return false, m, nil
	}
	entry, ok := m.deps.Recipes.Get(name)
	if !ok {
		return false, m, nil
	}

	r, err := entry.Load()
	if err != nil {
		m.dialogs.Push(&dialog.Text{
			DialogID: "error",
			Title:    "Recipe load error",
			Body:     err.Error(),
			Theme:    m.theme,
		})
		return true, m, nil
	}

	// Pre-populate the "input" key from args (mirroring handleRegistryPrompt)
	// and also seed any single user_prompt param that has no default.
	prePopulated := make(map[string]string)
	if args != "" {
		prePopulated["input"] = args
		// If there is exactly one user_prompt parameter and the user passed
		// args, bind args to that parameter so simple one-param recipes just work
		// without a dialog: /myrecipe some text -> param filled from args.
		var userPromptParams []recipe.Parameter
		for _, p := range r.Parameters {
			if p.Requirement == recipe.RequirementUserPrompt {
				userPromptParams = append(userPromptParams, p)
			}
		}
		if len(userPromptParams) == 1 && prePopulated[userPromptParams[0].Name] == "" {
			prePopulated[userPromptParams[0].Name] = args
		}
	}

	// onComplete is called once all user_prompt parameters have been
	// collected. It renders the recipe and submits the result to the agent.
	onComplete := func(params map[string]string) (tea.Model, tea.Cmd) {
		rendered, rerr := recipe.Render(r, params)
		if rerr != nil {
			m.dialogs.Push(&dialog.Text{
				DialogID: "error",
				Title:    "Recipe render error",
				Body:     rerr.Error(),
				Theme:    m.theme,
			})
			return m, nil
		}
		return m.startRun(rendered)
	}

	collector := newRecipeParamCollector(m, r, prePopulated, onComplete)
	// Store the active collector on the model so handleKey can advance it
	// after each parameter dialog pops.
	m.recipeCollector = collector
	mod, cmd = collector.pushNextOrComplete(m)
	return true, mod, cmd
}

// relativeTime renders how long ago then was, relative to now, as a compact
// "just now" / "5m ago" / "3h ago" / "2d ago" label for the session switcher,
// matching the last-active column Claude Code and opencode show beside each
// session. Granularity coarsens as the gap widens (minutes, then hours, days,
// weeks, months, then years), so an aging session reads as "2mo ago" or "1y ago"
// rather than an unwieldy "60w ago"; a zero or future timestamp reads as "just
// now" so a freshly-created session never shows a negative or empty age. Months
// and years use the conventional 30- and 365-day approximations, which is ample
// precision for a last-active glance.
func relativeTime(then, now time.Time) string {
	if then.IsZero() {
		return "just now"
	}
	const day = 24 * time.Hour
	d := now.Sub(then)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < day:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	case d < 7*day:
		return fmt.Sprintf("%dd ago", int(d/day))
	case d < 30*day:
		return fmt.Sprintf("%dw ago", int(d/(7*day)))
	case d < 365*day:
		return fmt.Sprintf("%dmo ago", int(d/(30*day)))
	default:
		return fmt.Sprintf("%dy ago", int(d/(365*day)))
	}
}

// shortSessionID truncates a session id to a stable short form for display.
func shortSessionID(id string) string {
	if id == "" {
		return "new"
	}
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

// loadPromptRegistry builds the prompt registry from the standard global and
// project prompt directories. It never returns an error; a load failure yields
// an empty registry so a missing or malformed prompts directory cannot block
// TUI startup.
func loadPromptRegistry(cfg *config.Config) *config.PromptRegistry {
	reg, err := config.LoadPromptRegistry(promptDirs(cfg)...)
	if err != nil || reg == nil {
		empty, _ := config.LoadPromptRegistry()
		return empty
	}
	return reg
}

// promptDirs returns the directories scanned for custom prompts. The set is
// derived from the configured data directory when available; an empty slice is
// acceptable and yields an empty registry.
func promptDirs(cfg *config.Config) []string {
	if cfg == nil || cfg.Options.DataDir == "" {
		return nil
	}
	return []string{filepath.Join(cfg.Options.DataDir, "prompts")}
}
