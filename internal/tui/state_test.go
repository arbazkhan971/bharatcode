package tui

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/app"
	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/arbazkhan971/bharatcode/internal/tui/dialog"
	tea "github.com/charmbracelet/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

// TestState_StartsInInitBeforeSize proves a freshly constructed model sits on the
// transient init page until a terminal size arrives — the page is not resolved
// while dimensions (and the first-run check) are unknown.
func TestState_StartsInInitBeforeSize(t *testing.T) {
	t.Parallel()

	m := newModel(context.Background(), testDeps())
	require.Equal(t, uiInit, m.state, "a sized-unknown model is on the init page")
}

// TestState_ResolvesToLandingOnFirstSize proves the first WindowSizeMsg settles
// an empty, no-onboarding session onto the landing page (not chat), so a fresh
// launch shows the welcome panel rather than a blank transcript.
func TestState_ResolvesToLandingOnFirstSize(t *testing.T) {
	t.Parallel()

	m := newModel(context.Background(), testDeps())
	_, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	require.Equal(t, uiLanding, m.state, "an empty session lands after the first size")
	require.False(t, m.dialogs.Contains(onboardingDialogID), "no onboarding for a keyless-but-providerless config")
}

// TestState_SetStateUpdatesStateAndFocus proves setState moves both the page and
// the input/chat focus in one step — the single mutator the layout reads.
func TestState_SetStateUpdatesStateAndFocus(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	m.setState(uiChat, focusChat)
	require.Equal(t, uiChat, m.state)
	require.Equal(t, focusChat, m.focus)

	m.setState(uiLanding, focusInput)
	require.Equal(t, uiLanding, m.state)
	require.Equal(t, focusInput, m.focus)
}

// TestState_ResolvePageByContent proves the steady-state page is derived from the
// conversation: empty lands, while a launched turn or a restored persisted
// session chats.
func TestState_ResolvePageByContent(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	require.False(t, m.hasConversation())
	require.Equal(t, uiLanding, m.resolvePage(), "an empty transcript resolves to landing")

	m.turn = 1
	require.True(t, m.hasConversation())
	require.Equal(t, uiChat, m.resolvePage(), "a launched turn resolves to chat")

	m.turn = 0
	m.sessionPersisted = true
	require.Equal(t, uiChat, m.resolvePage(), "a restored persisted session resolves to chat")
}

// TestState_LandingShowsWelcomePanel proves the landing page renders the welcome
// panel in place of the transcript, and that leaving landing for chat replaces it
// with the (flowing) transcript region.
func TestState_LandingShowsWelcomePanel(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	require.Equal(t, uiLanding, m.state)
	require.Contains(t, m.renderMain(), "Ready when you are", "landing shows the welcome greeting")

	m.setState(uiChat, m.focus)
	require.NotContains(t, m.renderMain(), "Ready when you are", "the chat page drops the welcome panel")
}

// TestState_FirstPromptEntersChat proves submitting the first prompt moves the
// shell off landing onto the chat page — the landing→chat transition.
func TestState_FirstPromptEntersChat(t *testing.T) {
	provider := &scriptedProvider{scripts: [][]llm.Event{
		{llm.DeltaTextEvent{Text: "ok"}, llm.EndEvent{}},
	}}
	h := newAgentHarness(t, provider)
	m := h.model

	require.Equal(t, uiLanding, m.state, "the harness model lands before any turn")
	h.submit(t, "do a thing")
	// launchTurn sets the chat page synchronously inside the submit Update, before
	// the background run completes, so the page has already moved here.
	require.Equal(t, uiChat, m.state, "the first prompt enters the chat page")
	h.drain(t, func() bool { return !m.running })
	require.Equal(t, uiChat, m.state, "the shell stays on chat once the turn finishes")
}

// TestState_OnboardingPageEntersAndLeaves proves first-run setup is its own page:
// the onboarding deps drive the shell to uiOnboarding on first size, and
// dismissing the dialog with esc settles back onto the landing page rather than
// stranding the shell on onboarding with no dialog.
func TestState_OnboardingPageEntersAndLeaves(t *testing.T) {
	withMemKeyring(t)
	m := newModel(context.Background(), onboardingDeps())
	_, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	require.True(t, m.dialogs.Contains(onboardingDialogID), "onboarding deps open the setup dialog")
	require.Equal(t, uiOnboarding, m.state, "the open setup dialog puts the shell on the onboarding page")

	_, _ = m.Update(keySpecial("esc", tea.KeyEscape))
	require.False(t, m.dialogs.Contains(onboardingDialogID), "esc dismisses the setup dialog")
	require.Equal(t, uiLanding, m.state, "dismissing setup settles onto the landing page")
}

// TestModalFocus_DialogConsumesKeysAwayFromInput proves a modal on the stack is a
// focus-managed layer: while it is on top, typed keys are routed to the dialog
// and never reach the prompt buffer, and a dismissal key pops it so input
// resumes.
func TestModalFocus_DialogConsumesKeysAwayFromInput(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	require.Equal(t, 0, m.input.Len())

	m.dialogs.Push(&dialog.Text{DialogID: "note", Title: "Heads up", Body: "info", Theme: m.theme})
	require.Equal(t, 1, m.dialogs.Len())

	// A printable key is consumed by the dialog layer, not inserted into the prompt.
	_, _ = m.Update(keyText("a"))
	require.Equal(t, 0, m.input.Len(), "keys must not reach the prompt while a modal is focused")
	require.Equal(t, 1, m.dialogs.Len(), "a non-dismiss key keeps the modal on the stack")

	// Esc dismisses the modal, returning focus to the prompt.
	_, _ = m.Update(keySpecial("esc", tea.KeyEscape))
	require.Equal(t, 0, m.dialogs.Len(), "esc pops the focused modal")

	// With the modal gone, typing reaches the prompt again.
	_, _ = m.Update(keyText("a"))
	require.Equal(t, 1, m.input.Len(), "the prompt accepts input once the modal is dismissed")
}

// TestHeaderInfo_YoloUsesPerSessionState asserts the header info strip reads
// the per-session yolo flag (m.status.Yolo) rather than the global (now-dead)
// Workspace.Yolo() flag. This covers the M3 fix: the header must match the
// status bar, which is updated by loadTab/restoreSession via SessionYolo.
//
// m.status.Yolo is the canonical per-session yolo field; loadTab and
// restoreSession populate it from Workspace.SessionYolo so it is always
// session-scoped. headerInfo reads m.status.Yolo directly — no per-render DB
// call — so the header stays correct at zero query cost.
func TestHeaderInfo_YoloUsesPerSessionState(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)

	// With a "new" / no session the header must track m.status.Yolo.
	m.sessionID = "new"
	m.status.Yolo = false
	require.False(t, m.headerInfo().Yolo,
		"header yolo must be false when status.Yolo is false and session is unpersisted")

	m.status.Yolo = true
	require.True(t, m.headerInfo().Yolo,
		"header yolo must reflect status.Yolo for an unpersisted session")

	// For a persisted session, m.status.Yolo is set by loadTab/restoreSession
	// from Workspace.SessionYolo — so it already holds the per-session value.
	// headerInfo must return whatever m.status.Yolo holds, never Workspace.Yolo().
	m.sessionID = "sess-abc-123"
	m.sessionPersisted = true
	m.status.Yolo = false // as loadTab would set it via SessionYolo (no grant)
	require.False(t, m.headerInfo().Yolo,
		"header yolo must match m.status.Yolo (per-session) for a persisted session")

	m.status.Yolo = true // as loadTab would set it after a /yolo grant
	require.True(t, m.headerInfo().Yolo,
		"header yolo must follow m.status.Yolo when the session has the grant")
}

// countingWorkspace wraps fakeWorkspace and counts calls to SessionState. It
// is used to prove that headerInfo never calls SessionState during rendering —
// the regression guard for the per-render DB query bug.
type countingWorkspace struct {
	*fakeWorkspace
	// sessionStateCalls is incremented atomically on every SessionState call.
	sessionStateCalls atomic.Int64
	// cwdVal is returned by Cwd() so tests can verify headerInfo uses it.
	cwdVal string
}

// Cwd returns cwdVal so tests can assert the header shows the correct cwd.
func (cw *countingWorkspace) Cwd() string { return cw.cwdVal }

// SessionState increments the call counter and delegates to fakeWorkspace.
// Any non-zero count during headerInfo calls is a regression.
func (cw *countingWorkspace) SessionState(ctx context.Context, sessionID string) (app.SessionState, error) {
	cw.sessionStateCalls.Add(1)
	return cw.fakeWorkspace.SessionState(ctx, sessionID)
}

// TestHeaderInfo_NoSessionStateCallPerRender is the regression guard: it
// proves that headerInfo never calls Workspace.SessionState, regardless of
// session state. Per-render DB queries (SQLite ChangedFiles) were the
// regression; all fields must come from in-memory cache.
func TestHeaderInfo_NoSessionStateCallPerRender(t *testing.T) {
	t.Parallel()

	deps := testDeps()
	cw := &countingWorkspace{
		fakeWorkspace: deps.Workspace.(*fakeWorkspace),
		cwdVal:        "/project/root",
	}
	deps.Workspace = cw

	m := newModel(context.Background(), deps)
	_, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	// Unpersisted "new" session — multiple headerInfo calls must not touch SessionState.
	m.sessionID = "new"
	m.sessionPersisted = false
	m.status.Yolo = true
	for i := 0; i < 5; i++ {
		info := m.headerInfo()
		require.Equal(t, "/project/root", info.Cwd,
			"headerInfo must use Workspace.Cwd() for the working directory")
		require.True(t, info.Yolo,
			"headerInfo must reflect m.status.Yolo for an unpersisted session")
	}
	require.Zero(t, cw.sessionStateCalls.Load(),
		"headerInfo must not call SessionState for an unpersisted session")

	// Persisted session — the regression case: multiple headerInfo calls (as
	// renderMain + headerExtraRows produce per frame) must not query the DB.
	m.sessionID = "sess-render-test"
	m.sessionPersisted = true
	m.status.Yolo = false
	m.changedFiles = 3
	cw.sessionStateCalls.Store(0) // reset counter for this sub-case
	for i := 0; i < 10; i++ {
		info := m.headerInfo()
		require.Equal(t, "/project/root", info.Cwd,
			"persisted session: headerInfo must use Workspace.Cwd(), not SessionState")
		require.False(t, info.Yolo,
			"persisted session: headerInfo must use m.status.Yolo (per-session), not SessionState")
		require.Equal(t, 3, info.Changed,
			"persisted session: headerInfo must use the cached m.changedFiles count")
	}
	require.Zero(t, cw.sessionStateCalls.Load(),
		"headerInfo must not call SessionState on every render — this is the regression guard")
}

// TestHeaderInfo_CwdAndChangedReflectCachedState verifies that the cwd and
// changed-file count rendered by headerInfo match the cheap in-memory sources:
// Workspace.Cwd() for the directory and m.changedFiles (refreshed at turn end
// by refreshChangedCount) for the file count.
func TestHeaderInfo_CwdAndChangedReflectCachedState(t *testing.T) {
	t.Parallel()

	deps := testDeps()
	cw := &countingWorkspace{
		fakeWorkspace: deps.Workspace.(*fakeWorkspace),
		cwdVal:        "/my/project",
	}
	deps.Workspace = cw

	m := newModel(context.Background(), deps)
	_, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	m.sessionID = "sess-cache-test"
	m.sessionPersisted = true
	m.changedFiles = 7

	info := m.headerInfo()
	require.Equal(t, "/my/project", info.Cwd,
		"headerInfo cwd must come from Workspace.Cwd()")
	require.Equal(t, 7, info.Changed,
		"headerInfo changed count must come from the cached m.changedFiles field")
	require.Zero(t, cw.sessionStateCalls.Load(),
		"headerInfo must not call SessionState when reading cached fields")
}

// TestModalFocus_PermissionRoutesThroughSeam proves a permission request arriving
// on the consolidated stream renders as a focused modal whose y/n keys answer
// through the workspace seam — the request's Reply channel receives the decision.
func TestModalFocus_PermissionRoutesThroughSeam(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)
	reply := make(chan pubsub.PermissionDecision, 1)
	req := pubsub.PermissionRequest{Tool: "bash", Reason: "run a command", Reply: reply}

	_, _ = m.Update(permissionRequestMsg(req))
	require.True(t, m.dialogs.Contains("permission"), "a permission request pushes the permission modal")

	// Approving with 'y' answers through the seam on the request's Reply channel.
	_, _ = m.Update(keyText("y"))
	select {
	case dec := <-reply:
		require.True(t, dec.Approved, "y must approve the request through the seam")
	default:
		t.Fatal("approving the permission modal must send a decision on the reply channel")
	}
	require.False(t, m.dialogs.Contains("permission"), "answering pops the permission modal")
}
