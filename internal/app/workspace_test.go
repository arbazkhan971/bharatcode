package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/arbazkhan971/bharatcode/internal/agent"
	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/db"
	"github.com/arbazkhan971/bharatcode/internal/filetracker"
	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/permission"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/arbazkhan971/bharatcode/internal/session"
	"github.com/arbazkhan971/bharatcode/internal/tools"
)

// compile-time assertion: the concrete impl satisfies the seam.
var _ Workspace = (*appWorkspace)(nil)

// stubProvider is a minimal llm.Provider that replays one canned stream per
// turn. It carries just enough surface for the agent loop to complete a turn
// without contacting a real model, so the Workspace seam can be exercised
// end-to-end deterministically.
type stubProvider struct {
	name  string
	model string
	reply string
}

func (s *stubProvider) Name() string { return s.name }

func (s *stubProvider) Models() []llm.Model {
	return []llm.Model{{ID: s.model, Provider: s.name, ContextWindow: 8192, SupportsTools: true}}
}

func (s *stubProvider) SupportsTools() bool  { return true }
func (s *stubProvider) SupportsImages() bool { return false }

func (s *stubProvider) Stream(ctx context.Context, req llm.Request) (<-chan llm.Event, error) {
	ch := make(chan llm.Event, 4)
	ch <- llm.StartEvent{}
	ch <- llm.DeltaTextEvent{Text: s.reply}
	ch <- llm.EndEvent{Usage: llm.Usage{InputTokens: 1, OutputTokens: 1}}
	close(ch)
	return ch, nil
}

// openWorkspaceDB opens a fresh SQLite database in a temp dir for a workspace
// test and registers its cleanup.
func openWorkspaceDB(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "ws.db"))
	require.NoError(t, err)
	t.Cleanup(func() { database.Close() })
	return database
}

// newTestWorkspace assembles a Workspace over a partial App wired with only the
// fields the seam touches (consolidated stream, permission checker, session
// repo, working directory) plus a real agent Loop backed by a stub provider. It
// returns the workspace, the underlying app, the live loop, and the stub
// provider so individual tests can assert against the concrete pieces. The
// fan-in and bus are torn down on cleanup.
func newTestWorkspace(t *testing.T) (Workspace, *App, *agent.Loop, *stubProvider) {
	t.Helper()

	bus := newBus()
	ctx := context.Background()
	ui := FanIn(ctx, bus)
	t.Cleanup(func() {
		ui.Close()
		bus.Close()
	})

	repo := session.NewRepo(openWorkspaceDB(t))
	checker := permission.New(&config.Config{}, bus.Permission)

	app := &App{
		Bus:        bus,
		UI:         ui,
		Sessions:   repo,
		Permission: checker,
		workDir:    "/tmp/bc-workspace",
	}

	prov := &stubProvider{name: "stub", model: "stub-model", reply: "ok"}
	loop := agent.New(agent.Config{
		Name:     "coder",
		Model:    "stub-model",
		Provider: prov,
		Tools:    tools.NewRegistry(tools.Dependencies{}),
		Sessions: repo,
		Bus:      bus.Agent,
	})

	return NewWorkspace(app, loop), app, loop, prov
}

// TestWorkspaceSubscribeConsolidated asserts the seam's Subscribe delivers
// events from the consolidated UI stream: an agent event published on the bus
// arrives wrapped as a UIEventAgent.
func TestWorkspaceSubscribeConsolidated(t *testing.T) {
	ws, app, _, _ := newTestWorkspace(t)

	ch, cancel := ws.Subscribe()
	defer cancel()

	want := agent.Event{Kind: agent.EventToolCalled, ToolName: "bash"}
	app.Bus.Agent.Publish(context.Background(), want)

	ev := recvUIEvent(t, ch)
	require.Equal(t, UIEventAgent, ev.Kind)
	require.Equal(t, want, ev.Agent)
}

// TestWorkspaceGrantPermission asserts GrantPermission sends an approving
// decision on the request's Reply channel, optionally remembering it.
func TestWorkspaceGrantPermission(t *testing.T) {
	ws, _, _, _ := newTestWorkspace(t)

	reply := make(chan pubsub.PermissionDecision, 1)
	req := pubsub.PermissionRequest{Tool: "edit", Reply: reply}

	ws.GrantPermission(req, true)

	select {
	case dec := <-reply:
		require.True(t, dec.Approved)
		require.True(t, dec.Remember)
	case <-time.After(time.Second):
		t.Fatal("GrantPermission did not send a decision on Reply")
	}
}

// TestWorkspaceDenyPermission asserts DenyPermission sends a denying decision
// carrying the supplied reason.
func TestWorkspaceDenyPermission(t *testing.T) {
	ws, _, _, _ := newTestWorkspace(t)

	reply := make(chan pubsub.PermissionDecision, 1)
	req := pubsub.PermissionRequest{Tool: "bash", Reply: reply}

	ws.DenyPermission(req, "blocked by policy")

	select {
	case dec := <-reply:
		require.False(t, dec.Approved)
		require.Equal(t, "blocked by policy", dec.Reason)
	case <-time.After(time.Second):
		t.Fatal("DenyPermission did not send a decision on Reply")
	}
}

// TestWorkspacePermissionNilReply asserts a malformed request with a nil Reply
// channel is a safe no-op rather than a panic, for both grant and deny.
func TestWorkspacePermissionNilReply(t *testing.T) {
	ws, _, _, _ := newTestWorkspace(t)

	require.NotPanics(t, func() {
		ws.GrantPermission(pubsub.PermissionRequest{Tool: "edit"}, true)
		ws.DenyPermission(pubsub.PermissionRequest{Tool: "edit"}, "no")
	})
}

// TestWorkspaceYoloRoundTrip asserts SetYolo flips the checker's yolo state and
// Yolo reads it back through the seam.
func TestWorkspaceYoloRoundTrip(t *testing.T) {
	ws, app, _, _ := newTestWorkspace(t)

	require.False(t, ws.Yolo(), "yolo defaults off")

	ws.SetYolo(true)
	require.True(t, ws.Yolo())
	require.True(t, app.Permission.Yolo(), "underlying checker reflects the toggle")

	ws.SetYolo(false)
	require.False(t, ws.Yolo())
}

// TestWorkspaceSessionYoloPerSession asserts SetSessionYolo/SessionYolo scope
// auto-approval to one session and never flip the global yolo switch.
func TestWorkspaceSessionYoloPerSession(t *testing.T) {
	ws, app, _, _ := newTestWorkspace(t)

	require.False(t, ws.SessionYolo("a"))
	ws.SetSessionYolo("a", true)
	require.True(t, ws.SessionYolo("a"))
	require.False(t, ws.SessionYolo("b"), "auto-approve is per-session, not global")
	require.False(t, app.Permission.Yolo(), "per-session yolo must not flip the global flag")

	// Global yolo, when set, makes every session report yolo.
	ws.SetYolo(true)
	require.True(t, ws.SessionYolo("b"))
	ws.SetYolo(false)

	ws.SetSessionYolo("a", false)
	require.False(t, ws.SessionYolo("a"))
}

// TestWorkspaceSessionState asserts the live snapshot bundles model, provider,
// cwd, per-session yolo, and the session's changed files, reading through to the
// loop, checker, and file tracker.
func TestWorkspaceSessionState(t *testing.T) {
	bus := newBus()
	ctx := context.Background()
	ui := FanIn(ctx, bus)
	t.Cleanup(func() {
		ui.Close()
		bus.Close()
	})

	database := openWorkspaceDB(t)
	repo := session.NewRepo(database)
	checker := permission.New(&config.Config{}, bus.Permission)
	ftBus := pubsub.NewTopic[filetracker.Change]("ws_ft", 16)
	t.Cleanup(func() { ftBus.Close() })
	tracker := filetracker.NewTracker(database, ftBus)

	app := &App{
		DB:          database,
		Bus:         bus,
		UI:          ui,
		Sessions:    repo,
		Permission:  checker,
		FileTracker: tracker,
		workDir:     "/tmp/bc-workspace",
	}
	prov := &stubProvider{name: "stub", model: "stub-model", reply: "ok"}
	loop := agent.New(agent.Config{
		Name:     "coder",
		Model:    "stub-model",
		Provider: prov,
		Tools:    tools.NewRegistry(tools.Dependencies{}),
		Sessions: repo,
		Bus:      bus.Agent,
	})
	ws := NewWorkspace(app, loop)

	require.NoError(t, ws.CreateSession(ctx, &session.Session{ID: "ss1", ProjectPath: "/tmp/p", Agent: "coder"}))

	st, err := ws.SessionState(ctx, "ss1")
	require.NoError(t, err)
	require.Equal(t, "ss1", st.SessionID)
	require.Equal(t, "stub-model", st.Model)
	require.Equal(t, "stub", st.Provider)
	require.Equal(t, "/tmp/bc-workspace", st.Cwd)
	require.False(t, st.Yolo)
	require.Empty(t, st.ChangedFiles)

	// Per-session yolo is reflected in the snapshot.
	ws.SetSessionYolo("ss1", true)
	st, err = ws.SessionState(ctx, "ss1")
	require.NoError(t, err)
	require.True(t, st.Yolo)

	// A recorded write surfaces as a changed file.
	f := filepath.Join(t.TempDir(), "a.go")
	_, err = tracker.RecordWrite(ctx, "ss1", f, nil, []byte("package main"))
	require.NoError(t, err)

	st, err = ws.SessionState(ctx, "ss1")
	require.NoError(t, err)
	require.Len(t, st.ChangedFiles, 1)
	require.Contains(t, st.ChangedFiles[0], "a.go")
}

// TestWorkspaceRunState asserts the run-state accessors read through to the live
// loop and the App: the current model and provider track a SetModel swap, and
// Cwd returns the App's working directory.
func TestWorkspaceRunState(t *testing.T) {
	ws, _, loop, prov := newTestWorkspace(t)

	require.Equal(t, "stub-model", ws.CurrentModel())
	require.Same(t, prov, ws.CurrentProvider())
	require.Equal(t, "/tmp/bc-workspace", ws.Cwd())

	// A model swap on the live loop must be visible through the seam.
	next := &stubProvider{name: "stub2", model: "stub-model-2", reply: "ok"}
	loop.SetModel("stub-model-2", next)
	require.Equal(t, "stub-model-2", ws.CurrentModel())
	require.Same(t, next, ws.CurrentProvider())
}

// TestWorkspaceSessionOps asserts every session operation on the seam delegates
// to the underlying repository: create, get, list, rename, append, and read
// back messages.
func TestWorkspaceSessionOps(t *testing.T) {
	ws, _, _, _ := newTestWorkspace(t)
	ctx := context.Background()

	sess := &session.Session{ID: "s1", ProjectPath: "/tmp/p", Title: "first", Agent: "coder"}
	require.NoError(t, ws.CreateSession(ctx, sess))

	got, err := ws.GetSession(ctx, "s1")
	require.NoError(t, err)
	require.Equal(t, "first", got.Title)

	require.NoError(t, ws.SetSessionTitle(ctx, "s1", "renamed"))
	got, err = ws.GetSession(ctx, "s1")
	require.NoError(t, err)
	require.Equal(t, "renamed", got.Title)

	list, err := ws.ListSessions(ctx, session.ListFilter{})
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, "s1", list[0].ID)

	msg := message.Message{
		SessionID: "s1",
		Role:      message.RoleUser,
		Content:   []message.ContentBlock{message.TextBlock{Text: "hello"}},
		CreatedAt: time.Now().UTC(),
	}
	require.NoError(t, ws.AppendMessage(ctx, "s1", msg))

	msgs, err := ws.Messages(ctx, "s1")
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	require.Equal(t, message.RoleUser, msgs[0].Role)
}

// TestWorkspaceSteerNoRun asserts Steer returns false when no turn is in flight
// (the caller should then start a fresh Prompt) and Interrupt is a safe no-op.
func TestWorkspaceSteerNoRun(t *testing.T) {
	ws, _, _, _ := newTestWorkspace(t)

	require.False(t, ws.Steer("course correct"), "Steer must report not-queued when no turn is live")
	require.NotPanics(t, ws.Interrupt, "Interrupt must be safe with no active turn")
}

// TestWorkspacePromptDrivesTurn is the end-to-end check that the seam wires the
// prompt path through to the live loop and back out the consolidated stream:
// Prompt runs a turn against the stub provider, and the turn-finished transition
// surfaces on Subscribe as a UIEventAgent.
func TestWorkspacePromptDrivesTurn(t *testing.T) {
	ws, _, _, _ := newTestWorkspace(t)
	ctx := context.Background()

	// A session must exist before a turn appends to it.
	require.NoError(t, ws.CreateSession(ctx, &session.Session{ID: "run1", ProjectPath: "/tmp/p", Agent: "coder"}))

	ch, cancel := ws.Subscribe()
	defer cancel()

	userMsg := message.Message{
		SessionID: "run1",
		Role:      message.RoleUser,
		Content:   []message.ContentBlock{message.TextBlock{Text: "do it"}},
	}
	require.NoError(t, ws.Prompt(ctx, "run1", userMsg))

	// Drain the consolidated stream until the turn-finished event lands. The
	// loop also emits a turn-started and an LLM-response event ahead of it.
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Kind == UIEventAgent && ev.Agent.Kind == agent.EventTurnFinished {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for turn-finished on the consolidated stream")
		}
	}
}
