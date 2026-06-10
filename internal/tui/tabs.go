package tui

import (
	"fmt"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/tui/chat"
	"github.com/arbazkhan971/bharatcode/internal/tui/dialog"
	tea "github.com/charmbracelet/bubbletea/v2"
	"github.com/charmbracelet/lipgloss/v2"
)

// maxTabs bounds the number of concurrently open session tabs so the tab bar
// stays readable on a narrow terminal.
const maxTabs = 9

// tab holds the per-session state that swaps when the user switches tabs. Each
// tab owns its own chat list and session identity, so two tabs never share a
// transcript. The active tab's fields are mirrored onto the model (m.chat,
// m.sessionID, ...) for the rest of the TUI to read unchanged; switching saves
// the live fields back into the current tab and loads the target tab's.
type tab struct {
	chat             *chat.List
	sessionID        string
	sessionPersisted bool
	// firstPrompt is the first user prompt submitted in this tab; it titles the
	// tab in the /tabs listing. Empty until the tab's first turn.
	firstPrompt   string
	goal          string
	goalActive    bool
	goalIteration int
	turn          int
	queueCounter  int
	chatScroll    int
	search        searchState
	// statusModel and statusAgent retain the model/agent labels shown in the
	// status bar so a tab restored from a different session keeps its identity.
	statusModel string
	statusAgent string
	// statusGoal retains the goal-progress segment shown in the status bar so a
	// tab mid goal-loop keeps its indicator when reactivated.
	statusGoal string
	// costINR retains the per-tab session spend mirrored into the footer.
	costINR float64
	// changedFiles retains the count of files modified by this tab's session so
	// the header info strip shows the correct count after a tab switch without
	// waiting for the next turn to refresh it.
	changedFiles int
	// lastTurnTokens retains the formatted token-count segment from the most
	// recently completed turn so the status bar shows the right stats after a
	// tab switch (not the counts from the previously active tab).
	lastTurnTokens string
}

// initTabs seeds the model with a single tab that adopts the freshly built
// active state. It is called once during newModel after the initial chat,
// status, and footer are wired, so the default single-tab path is identical to
// the prior behavior.
func (m *model) initTabs() {
	m.tabs = []tab{m.snapshotTab()}
	m.activeTab = 0
}

// tabChangedFiles returns the changedFiles count for tab index. For the active
// tab it reads the live model field (which loadTab and turn-end updates keep
// current); other tabs read their snapshot.
func (m *model) tabChangedFiles(index int) int {
	if index == m.activeTab {
		return m.changedFiles
	}
	if index >= 0 && index < len(m.tabs) {
		return m.tabs[index].changedFiles
	}
	return 0
}

// snapshotTab captures the model's live per-session fields into a tab value.
func (m *model) snapshotTab() tab {
	return tab{
		chat:             m.chat,
		sessionID:        m.sessionID,
		sessionPersisted: m.sessionPersisted,
		firstPrompt:      m.tabFirstPrompt,
		goal:             m.goal,
		goalActive:       m.goalActive,
		goalIteration:    m.goalIteration,
		turn:             m.turn,
		queueCounter:     m.queueCounter,
		chatScroll:       m.chatScroll,
		search:           m.search,
		statusModel:      m.status.Model,
		statusAgent:      m.status.Agent,
		statusGoal:       m.status.Goal,
		costINR:          m.footer.CostINR,
		changedFiles:     m.changedFiles,
		lastTurnTokens:   m.lastTurnTokens,
	}
}

// saveActiveTab writes the model's live per-session fields back into the
// currently active tab slot so a subsequent switch restores them intact.
func (m *model) saveActiveTab() {
	if m.activeTab < 0 || m.activeTab >= len(m.tabs) {
		return
	}
	m.tabs[m.activeTab] = m.snapshotTab()
}

// loadTab makes tab index the active view: it mirrors the tab's per-session
// fields onto the model and onto the status bar and footer. It assumes the
// caller has already saved the outgoing tab.
func (m *model) loadTab(index int) {
	if index < 0 || index >= len(m.tabs) {
		return
	}
	t := m.tabs[index]
	m.activeTab = index
	m.chat = t.chat
	m.sessionID = t.sessionID
	m.sessionPersisted = t.sessionPersisted
	m.tabFirstPrompt = t.firstPrompt
	m.goal = t.goal
	m.goalActive = t.goalActive
	m.goalIteration = t.goalIteration
	m.turn = t.turn
	m.queueCounter = t.queueCounter
	m.chatScroll = t.chatScroll
	m.search = t.search
	m.status.Model = t.statusModel
	m.status.Agent = t.statusAgent
	m.status.SessionID = t.sessionID
	m.status.Goal = t.statusGoal
	m.footer.SessionID = t.sessionID
	m.footer.CostINR = t.costINR
	m.changedFiles = t.changedFiles
	m.lastTurnTokens = t.lastTurnTokens
	// Yolo is per-session: a persisted tab reflects its session's auto-approval
	// state; an unpersisted "new" tab carries no grant yet, so the indicator clears.
	if m.deps.Workspace != nil && t.sessionPersisted {
		m.status.Yolo = m.deps.Workspace.SessionYolo(t.sessionID)
	} else {
		m.status.Yolo = false
	}
	// The newly active tab may hold a conversation or be a fresh empty tab, so
	// re-derive the content page from its restored state — a freshly opened tab
	// lands, a tab with turns shows its transcript.
	m.syncPage()
}

// newTab opens a fresh session tab and switches to it. The new tab starts as an
// unpersisted "new" session with an empty chat, exactly like a freshly launched
// TUI, so its first prompt creates a real session row. It returns a command
// that refreshes the ledger footer for the now-active (empty) session. When the
// tab limit is reached it surfaces an informational note and does nothing.
func (m *model) newTab() tea.Cmd {
	if m.running || m.goalActive {
		m.note("Finish or interrupt the current turn before opening a tab.")
		return nil
	}
	if len(m.tabs) >= maxTabs {
		m.note(fmt.Sprintf("Tab limit reached (%d).", maxTabs))
		return nil
	}
	m.saveActiveTab()

	chatList := chat.New()
	chatList.EnableMarkdown(m.theme.Markdown)
	chatList.EnableDiff(m.theme)
	modelName, agentName := initialIdentity(m.deps.Cfg)
	fresh := tab{
		chat:        chatList,
		sessionID:   "new",
		statusModel: modelName,
		statusAgent: agentName,
	}
	m.tabs = append(m.tabs, fresh)
	m.loadTab(len(m.tabs) - 1)
	return m.waitLedger()
}

// switchTab activates the tab at index, saving the current tab first. Switching
// to the already-active tab, or to an out-of-range index, is a no-op. It is
// refused while an agent turn is in flight so streamed output never lands in
// the wrong tab; the caller is told via a note. It returns a command that
// refreshes the ledger footer for the newly active session.
func (m *model) switchTab(index int) tea.Cmd {
	if index < 0 || index >= len(m.tabs) {
		return nil
	}
	if index == m.activeTab {
		return nil
	}
	if m.running || m.goalActive {
		m.note("Finish or interrupt the current turn before switching tabs.")
		return nil
	}
	m.saveActiveTab()
	m.loadTab(index)
	return m.waitLedger()
}

// nextTab cycles to the following tab, wrapping past the last back to the first.
// With a single open tab it is a no-op.
func (m *model) nextTab() tea.Cmd {
	if len(m.tabs) <= 1 {
		return nil
	}
	return m.switchTab((m.activeTab + 1) % len(m.tabs))
}

// prevTab cycles to the preceding tab, wrapping past the first back to the last.
// With a single open tab it is a no-op.
func (m *model) prevTab() tea.Cmd {
	if len(m.tabs) <= 1 {
		return nil
	}
	return m.switchTab((m.activeTab - 1 + len(m.tabs)) % len(m.tabs))
}

// closeTab removes the active tab and switches focus to a neighbor. The last
// remaining tab is never closed (so there is always a live chat/session); an
// attempt to close it surfaces a note instead. Closing a tab discards its
// in-memory transcript view only; any persisted session row is untouched and
// remains reachable through /sessions. It is refused while a turn is in flight.
func (m *model) closeTab() tea.Cmd {
	if len(m.tabs) <= 1 {
		m.note("Cannot close the last tab.")
		return nil
	}
	if m.running || m.goalActive {
		m.note("Finish or interrupt the current turn before closing a tab.")
		return nil
	}
	closing := m.activeTab
	m.tabs = append(m.tabs[:closing], m.tabs[closing+1:]...)
	next := closing
	if next >= len(m.tabs) {
		next = len(m.tabs) - 1
	}
	// loadTab reads m.activeTab only via the index argument, so set it fresh.
	m.activeTab = next
	m.loadTab(next)
	return m.waitLedger()
}

// note pushes a small informational dialog. It centralizes the one-off
// messages the tab commands surface so they share a consistent presentation.
func (m *model) note(body string) {
	m.dialogs.Push(&dialog.Text{DialogID: "tabs", Title: "Tabs", Body: body, Theme: m.theme})
}

// handleTabCommand implements the /tab slash family:
//
//	/tab            open a new tab and switch to it
//	/tab new        open a new tab and switch to it
//	/tab next       cycle to the next tab
//	/tab prev       cycle to the previous tab
//	/tab close      close the active tab
//	/tab list       show the open tabs (same as /tabs)
//	/tab N          switch to tab number N (1-based)
//
// An unrecognized argument surfaces a short usage note.
func (m *model) handleTabCommand(text string) (tea.Model, tea.Cmd) {
	arg := strings.TrimSpace(strings.TrimPrefix(text, "/tab"))
	switch {
	case arg == "", strings.EqualFold(arg, "new"):
		return m, m.newTab()
	case strings.EqualFold(arg, "next"):
		return m, m.nextTab()
	case strings.EqualFold(arg, "prev"), strings.EqualFold(arg, "previous"):
		return m, m.prevTab()
	case strings.EqualFold(arg, "close"):
		return m, m.closeTab()
	case strings.EqualFold(arg, "list"):
		return m.handleTabsList()
	}
	if n, ok := parseTabIndex(arg); ok {
		if n < 1 || n > len(m.tabs) {
			m.note(fmt.Sprintf("No tab %d (have %d).", n, len(m.tabs)))
			return m, nil
		}
		return m, m.switchTab(n - 1)
	}
	m.note("usage: /tab [new|next|prev|close|list|N]")
	return m, nil
}

// handleTabsList shows the open tabs with the active one marked. It is the
// target of both /tabs and /tab list.
func (m *model) handleTabsList() (tea.Model, tea.Cmd) {
	lines := make([]string, 0, len(m.tabs)+2)
	for i := range m.tabs {
		marker := "  "
		if i == m.activeTab {
			marker = "> "
		}
		lines = append(lines, fmt.Sprintf("%s%d: %s", marker, i+1, m.tabTitle(i)))
	}
	lines = append(lines, "", "Ctrl+T new · Ctrl+Right/Left switch · Ctrl+W close")
	m.dialogs.Push(&dialog.Text{DialogID: "tabs", Title: "Tabs", Body: strings.Join(lines, "\n"), Theme: m.theme})
	return m, nil
}

// tabTitleMaxLen bounds a content-derived tab title so a long opening prompt
// does not dominate the /tabs listing.
const tabTitleMaxLen = 48

// tabTitle returns a human-friendly label for tab index, used in the /tabs
// listing. It prefers the first line of the tab's opening user prompt — trimmed
// and truncated — so tabs are distinguishable by what they are about rather than
// by an opaque session id, matching how the session switchers in Claude Code and
// opencode title a conversation by its first message. A persisted tab still
// shows its short session id (in parentheses after a content title, or as the
// bare "session <id>" when there is no prompt yet) so the id stays available for
// /sessions; an unpersisted tab with no prompt falls back to "new session".
func (m *model) tabTitle(index int) string {
	snippet := firstLineSnippet(m.tabFirstPromptText(index), tabTitleMaxLen)
	if !m.tabPersisted(index) {
		if snippet != "" {
			return snippet
		}
		return "new session"
	}
	id := shortSessionID(m.tabSessionID(index))
	if snippet != "" {
		return snippet + "  (" + id + ")"
	}
	return "session " + id
}

// tabFirstPromptText returns the first user prompt for tab index, preferring the
// prompt captured when the tab's first turn launched and falling back to the
// first user message in its restored transcript. For the active tab it reads the
// live model field, which is authoritative (launchTurn updates it without
// re-snapshotting the tab); other tabs read their snapshot. A restored session
// has no captured prompt, so its opening user message backs the title instead.
func (m *model) tabFirstPromptText(index int) string {
	prompt := ""
	if index == m.activeTab {
		prompt = m.tabFirstPrompt
	} else if index >= 0 && index < len(m.tabs) {
		prompt = m.tabs[index].firstPrompt
	}
	if prompt != "" {
		return prompt
	}
	if index >= 0 && index < len(m.tabs) && m.tabs[index].chat != nil {
		return m.tabs[index].chat.FirstUserText()
	}
	return ""
}

// firstLineSnippet reduces s to a single compact line for a tab title: the first
// non-blank line, with inner runs of whitespace collapsed to single spaces, then
// truncated to maxLen runes with a trailing ellipsis when cut. It returns "" when
// s holds no non-blank text. Truncation is rune-wise so a multi-byte character is
// never split into invalid UTF-8.
func firstLineSnippet(s string, maxLen int) string {
	var line string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			line = l
			break
		}
	}
	line = strings.Join(strings.Fields(line), " ")
	if line == "" {
		return ""
	}
	if runes := []rune(line); maxLen > 0 && len(runes) > maxLen {
		return string(runes[:maxLen-1]) + "…"
	}
	return line
}

// parseTabIndex parses a 1-based tab number from s, reporting whether s was a
// bare positive integer.
func parseTabIndex(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, true
}

// tabSessionID returns the session identifier shown for tab index. For the
// active tab it reads the live model field, which is authoritative: ensureSession
// and restoreSession update m.sessionID without re-snapshotting, so the snapshot
// in m.tabs[activeTab] can lag behind until the next save. Other tabs read their
// snapshot, which is current because they are inactive.
func (m *model) tabSessionID(index int) string {
	if index == m.activeTab {
		return m.sessionID
	}
	return m.tabs[index].sessionID
}

// tabPersisted reports whether tab index has a persisted session, reading the
// live model field for the active tab (see tabSessionID for why).
func (m *model) tabPersisted(index int) bool {
	if index == m.activeTab {
		return m.sessionPersisted
	}
	return m.tabs[index].sessionPersisted
}

// tabLabel renders one tab's short label for the tab bar: its 1-based number and
// a compact session identifier (or "new" for an unpersisted tab).
func (m *model) tabLabel(index int) string {
	return fmt.Sprintf("%d:%s", index+1, shortSessionID(m.tabSessionID(index)))
}

// renderTabBar renders the tab bar shown above the chat when more than one tab
// is open. With a single tab it returns "" so the default layout is unchanged.
// The active tab is themed with the header style; the rest use the muted status
// style. When the styled bar would exceed width, whole tab labels are dropped
// from the end and a "+N" overflow marker is appended, so the line never splits
// an ANSI escape mid-sequence. The active tab is always kept visible.
func (m *model) renderTabBar(width int) string {
	if len(m.tabs) <= 1 || width <= 0 {
		return ""
	}
	styled := make([]string, len(m.tabs))
	for i := range m.tabs {
		label := " " + m.tabLabel(i) + " "
		if i == m.activeTab {
			styled[i] = m.theme.Header.Render(label)
		} else {
			styled[i] = m.theme.Status.Render(label)
		}
	}
	return m.fitTabBar(styled, width)
}

// fitTabBar joins the styled tab labels with single-space separators so the
// result never exceeds width. The active tab is always kept visible; a contiguous
// window of tabs is grown around it (forward first, then backward) while the
// total fits, and a styled "+N" marker counts the tabs hidden on either side.
// Each separator and the marker are measured exactly so the line never splits an
// ANSI escape and never overflows.
func (m *model) fitTabBar(styled []string, width int) string {
	n := len(styled)
	active := m.activeTab
	if active < 0 || active >= n {
		active = 0
	}

	// markerWidth measures the rendered width of a "+k" marker plus its leading
	// separator space, for k hidden tabs. It is recomputed as the window grows.
	markerWidth := func(hidden int) int {
		if hidden <= 0 {
			return 0
		}
		return 1 + lipgloss.Width(m.theme.Status.Render(fmt.Sprintf("+%d", hidden)))
	}

	lo, hi := active, active // inclusive window [lo, hi]
	used := lipgloss.Width(styled[active])

	for {
		grew := false
		// Prefer extending forward so newly opened tabs (appended at the end)
		// stay visible.
		if hi < n-1 {
			next := 1 + lipgloss.Width(styled[hi+1]) // separator + label
			// Account for the marker shrinking as hi advances toward the end.
			if used+next+markerWidth((lo)+(n-1-(hi+1))) <= width {
				hi++
				used += next
				grew = true
			}
		}
		if lo > 0 {
			prev := 1 + lipgloss.Width(styled[lo-1])
			if used+prev+markerWidth((lo-1)+(n-1-hi)) <= width {
				lo--
				used += prev
				grew = true
			}
		}
		if !grew {
			break
		}
	}

	hidden := lo + (n - 1 - hi)
	line := strings.Join(styled[lo:hi+1], " ")
	if hidden > 0 {
		line += " " + m.theme.Status.Render(fmt.Sprintf("+%d", hidden))
	}
	return line
}
