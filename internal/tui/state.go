package tui

import (
	"context"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/tui/sidebar"
	"github.com/arbazkhan971/bharatcode/internal/util"
	"github.com/charmbracelet/lipgloss/v2"
)

// uiState enumerates the explicit page states the TUI shell can occupy. The
// state selects which top-level layout View renders, independent of the
// input/chat focus within a page; setState is the single mutator that moves
// between them, so every page transition reads from one place.
//
// The states form a small machine: a session starts in uiInit (size unknown),
// resolves on the first WindowSizeMsg into uiOnboarding (first-run setup),
// uiLanding (idle, empty transcript), or uiChat (a conversation is underway),
// and thereafter moves landing→chat on the first turn and onboarding→landing/chat
// when setup completes.
type uiState int

const (
	// uiInit is the transient startup page before the terminal size is known and
	// before the first-run onboarding check has run. It renders like the page it
	// will resolve into, so nothing flickers; the first WindowSizeMsg settles it.
	uiInit uiState = iota
	// uiOnboarding is the first-run setup page: the onboarding dialog is the
	// focused layer and the (empty) transcript behind it is inert.
	uiOnboarding
	// uiLanding is the idle pre-conversation page shown while the transcript is
	// empty — a centered welcome panel in place of the blank transcript, with the
	// prompt ready to accept the first message.
	uiLanding
	// uiChat is the active conversation page: the flowing transcript plus prompt.
	uiChat
)

// String renders the state name for diagnostics and tests.
func (s uiState) String() string {
	switch s {
	case uiInit:
		return "init"
	case uiOnboarding:
		return "onboarding"
	case uiLanding:
		return "landing"
	case uiChat:
		return "chat"
	default:
		return "unknown"
	}
}

// setState moves the shell to state and sets the input/chat focus in one step,
// so every page transition goes through a single mutator the layout reads. It is
// the seam the P2 shell is built on: callers name the page and the focus, and the
// render path switches on m.state rather than inferring the page from scattered
// flags. Setting the current state and focus again is a harmless no-op.
func (m *model) setState(state uiState, focus focusState) {
	m.state = state
	m.focus = focus
}

// resolvePage picks the steady-state page from the conversation: uiChat once a
// turn has run or a persisted session was restored, uiLanding while the
// transcript is still empty. Onboarding and init are entered explicitly and are
// never returned here — this only chooses between the two content pages.
func (m *model) resolvePage() uiState {
	if m.hasConversation() {
		return uiChat
	}
	return uiLanding
}

// hasConversation reports whether the active tab holds any conversation: a turn
// has been launched, a persisted session was restored into it, or the transcript
// already holds rendered items. A fresh tab (turn 0, unpersisted "new" session,
// empty transcript) has none, so it lands rather than showing an empty scroll
// area.
func (m *model) hasConversation() bool {
	return m.turn > 0 || m.sessionPersisted || (m.chat != nil && m.chat.Len() > 0)
}

// showLanding reports whether the welcome panel should replace the chat region
// this frame. It requires the landing page, a genuinely empty transcript, and
// no open file-tree panel — so a panel the user explicitly toggled, or any
// streamed content, takes precedence over the welcome placeholder even if the
// page label has not yet moved off landing.
func (m *model) showLanding() bool {
	return m.state == uiLanding && !m.filetree.visible && !m.hasConversation()
}

// syncPage re-derives the content page after a transition that may have changed
// the conversation state (a tab switch, an onboarding dismissal). It leaves the
// onboarding and init pages untouched — those are owned by explicit setState
// calls — and only settles landing↔chat, preserving the current focus.
func (m *model) syncPage() {
	switch m.state {
	case uiInit, uiOnboarding:
		return
	default:
		m.setState(m.resolvePage(), m.focus)
	}
}

// headerInfo assembles the top info strip for the current frame from the live
// run state: the active model and its provider, the working directory (home
// collapsed to "~"), the yolo affordance, and the changed-file count tracked at
// turn end.
//
// For a persisted session it calls Workspace.SessionState once to obtain a
// consistent snapshot — cwd, yolo, model, provider — so the strip reflects a
// single coherent point in time rather than assembling fields from separate
// calls that may race. For an unpersisted "new" session (empty or "new"
// sessionID), or when the snapshot call returns an error, it falls back to the
// piecemeal approach (m.deps.Workspace.Cwd() + m.status.Yolo) so a fresh tab
// always renders without error.
func (m *model) headerInfo() sidebar.Info {
	cwd := ""
	// Start from the in-memory per-session yolo value maintained by the TUI's
	// own toggle path. For a persisted session it will be overwritten by the
	// SessionState snapshot below; for an unpersisted "new" session it stays as
	// the correct fallback. The global Workspace.Yolo() flag is not consulted —
	// it is no longer set in production and must not be used here (M3 fix).
	yolo := m.status.Yolo
	model := m.status.Model
	provider := m.providerName(m.status.Model)
	changed := m.changedFiles

	if m.deps.Workspace == nil {
		return sidebar.Info{
			Theme:    m.theme,
			Model:    model,
			Provider: provider,
			Cwd:      util.ShortPath(cwd),
			Yolo:     yolo,
			Changed:  changed,
		}
	}

	// For a persisted session, obtain a single consistent snapshot from the
	// workspace seam. This makes the previously dead SessionState API live and
	// guarantees that cwd, yolo, model, and provider come from one atomic read
	// rather than several independent lookups that could interleave with an
	// in-flight model switch or yolo toggle.
	if m.sessionID != "" && m.sessionID != "new" {
		if st, err := m.deps.Workspace.SessionState(context.Background(), m.sessionID); err == nil {
			cwd = st.Cwd
			yolo = st.Yolo
			if st.Model != "" {
				model = st.Model
			}
			if st.Provider != "" {
				provider = st.Provider
			}
			if len(st.ChangedFiles) > 0 {
				changed = len(st.ChangedFiles)
			}
			return sidebar.Info{
				Theme:    m.theme,
				Model:    model,
				Provider: provider,
				Cwd:      util.ShortPath(cwd),
				Yolo:     yolo,
				Changed:  changed,
			}
		}
	}

	// Fallback path: unpersisted session or SessionState returned an error.
	// Read cwd and yolo through the individual seam methods exactly as before,
	// so a fresh tab (empty/"new" sessionID) renders correctly.
	cwd = m.deps.Workspace.Cwd()
	if m.sessionID != "" && m.sessionID != "new" {
		// SessionState errored; still try the piecemeal yolo for correctness.
		yolo = m.deps.Workspace.SessionYolo(m.sessionID)
	}
	return sidebar.Info{
		Theme:    m.theme,
		Model:    model,
		Provider: provider,
		Cwd:      util.ShortPath(cwd),
		Yolo:     yolo,
		Changed:  changed,
	}
}

// providerName returns the provider backing modelID, looked up in config. It
// returns "" when no model matches or no config is wired, so the header strip
// simply omits the provider segment rather than showing a stale or empty name.
func (m *model) providerName(modelID string) string {
	if m.deps.Cfg == nil || modelID == "" {
		return ""
	}
	for _, mod := range m.deps.Cfg.Models {
		if mod.ID == modelID {
			return mod.Provider
		}
	}
	return ""
}

// refreshChangedCount updates the cached count of files the session has changed,
// read from the file tracker. It is called at turn end (the only point the count
// can move) so the header strip can show the count without querying the tracker
// on every render frame. It is a no-op — leaving the previous count — when no
// tracker or persisted session is wired, or the query fails.
func (m *model) refreshChangedCount() {
	if m.deps.FileTracker == nil || !m.sessionPersisted || m.sessionID == "" {
		return
	}
	changed, err := m.deps.FileTracker.ChangedFiles(m.ctx, m.sessionID)
	if err != nil {
		return
	}
	m.changedFiles = len(changed)
}

// landingBody renders the idle welcome panel shown on the uiLanding page in place
// of the empty transcript. It centers a short greeting and the quick-start hints
// in the chat region so a fresh session reads as a deliberate landing rather than
// a blank screen. It returns "" for a non-positive region so the caller can fall
// back to the normal (empty) transcript render.
func (m *model) landingBody(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	greeting := m.theme.Header.Render("Ready when you are")
	hints := m.theme.Muted.Render(strings.Join([]string{
		"Type a message to start a turn",
		"/help lists commands · /keys lists shortcuts",
		"@ mentions a file · /model switches models",
	}, "\n"))
	panel := lipgloss.JoinVertical(lipgloss.Center, greeting, "", hints)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, panel)
}
