package tui

// fakeWorkspace is a test double for app.Workspace. It subscribes to an
// agent event topic and fans events through as app.UIEvent values, so the
// TUI's consolidated-stream machinery (ensureListening → m.eventCh) works
// exactly as it does in production without standing up the full app.App graph.
//
// Usage pattern:
//
//	bus := pubsub.NewTopic[agent.Event]("test", 256)
//	perm := permission.New(cfg, pubsub.NewTopic[pubsub.PermissionRequest]("test_perm", 16))
//	ws := newFakeWorkspace(context.Background(), bus, loop, perm, repo)
//
// The caller passes ws as Dependencies.Workspace. Events published onto bus
// arrive on the channel returned by ws.Subscribe() as UIEventAgent UIEvents.

import (
	"context"
	"sync"

	"github.com/arbazkhan971/bharatcode/internal/agent"
	"github.com/arbazkhan971/bharatcode/internal/app"
	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/permission"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/arbazkhan971/bharatcode/internal/session"
)

// fakeWorkspace implements app.Workspace for tests. It fans agent events from
// the provided bus into a consolidated UIEvent channel, mirroring what
// app.FanIn does in production.
type fakeWorkspace struct {
	loop     *agent.Loop
	perm     *permission.Checker
	sessions *session.Repo

	// out is the consolidated UIEvent topic the TUI subscribes to.
	out *pubsub.Topic[app.UIEvent]

	// cancel stops the pump goroutine started by newFakeWorkspace.
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// yolo is the toggle the TUI flips via SetYolo.
	yolo bool
}

// newFakeWorkspace constructs a fakeWorkspace that fans events from bus into
// a consolidated UIEvent channel. The pump goroutine runs until ctx is
// cancelled or the fakeWorkspace is garbage-collected. Pass t.Cleanup(ws.close)
// to stop the pump when the test ends.
func newFakeWorkspace(
	ctx context.Context,
	bus *pubsub.Topic[agent.Event],
	loop *agent.Loop,
	perm *permission.Checker,
	repo *session.Repo,
) *fakeWorkspace {
	pumpCtx, cancel := context.WithCancel(ctx)
	fw := &fakeWorkspace{
		loop:     loop,
		perm:     perm,
		sessions: repo,
		out:      pubsub.NewTopic[app.UIEvent]("fake_workspace_ui", 256),
		cancel:   cancel,
	}
	if bus != nil {
		ch, stopSub := bus.Subscribe()
		fw.wg.Add(1)
		go func() {
			defer fw.wg.Done()
			defer stopSub()
			for {
				select {
				case <-pumpCtx.Done():
					return
				case ev, ok := <-ch:
					if !ok {
						return
					}
					uiEv := app.AgentUIEvent(ev)
					if uiEv.Agent.Kind == agent.EventTurnFinished ||
						uiEv.Agent.Kind == agent.EventRunError ||
						uiEv.Agent.Kind == agent.EventLoopDetected ||
						uiEv.Agent.Kind == agent.EventAutoCompacted {
						fw.out.PublishBlocking(pumpCtx, uiEv)
					} else {
						fw.out.Publish(pumpCtx, uiEv)
					}
				}
			}
		}()
	}
	return fw
}

// close stops the pump goroutine and closes the output topic. Call from
// t.Cleanup to avoid goroutine leaks.
func (fw *fakeWorkspace) close() {
	fw.cancel()
	fw.wg.Wait()
	fw.out.Close()
}

// Subscribe returns a receive-only UIEvent channel and a cancel func, exactly
// as app.UIStream.Subscribe does. The TUI calls this once via ensureListening.
func (fw *fakeWorkspace) Subscribe() (<-chan app.UIEvent, func()) {
	return fw.out.Subscribe()
}

// Prompt is not exercised by the TUI tests that need a Workspace; the actual
// agent run is driven by m.runAgent (which calls loop.Run directly). Return nil
// to satisfy the interface.
func (fw *fakeWorkspace) Prompt(_ context.Context, _ string, _ message.Message) error {
	return nil
}

// Steer forwards to the live loop's Steer.
func (fw *fakeWorkspace) Steer(text string) (queued bool) {
	if fw.loop == nil {
		return false
	}
	return fw.loop.Steer(text)
}

// Interrupt forwards to the live loop's Interrupt.
func (fw *fakeWorkspace) Interrupt() {
	if fw.loop == nil {
		return
	}
	fw.loop.Interrupt()
}

// GrantPermission answers a pending permission request with approval.
func (fw *fakeWorkspace) GrantPermission(req pubsub.PermissionRequest, _ bool) {
	if req.Reply == nil {
		return
	}
	req.Reply <- pubsub.PermissionDecision{Approved: true}
}

// DenyPermission answers a pending permission request with denial.
func (fw *fakeWorkspace) DenyPermission(req pubsub.PermissionRequest, reason string) {
	if req.Reply == nil {
		return
	}
	req.Reply <- pubsub.PermissionDecision{Approved: false, Reason: reason}
}

// SetYolo toggles the yolo flag (and the permission checker when available).
func (fw *fakeWorkspace) SetYolo(on bool) {
	fw.yolo = on
	if fw.perm != nil {
		fw.perm.SetYolo(on)
	}
}

// Yolo reports the current yolo state.
func (fw *fakeWorkspace) Yolo() bool {
	return fw.yolo
}

// SetSessionYolo toggles per-session auto-approval on the checker when wired.
func (fw *fakeWorkspace) SetSessionYolo(sessionID string, on bool) {
	if fw.perm != nil {
		fw.perm.SetAutoApproveSession(sessionID, on)
	}
}

// SessionYolo reports per-session auto-approval, falling back to the local yolo
// flag when no checker is wired.
func (fw *fakeWorkspace) SessionYolo(sessionID string) bool {
	if fw.perm != nil {
		return fw.perm.Yolo() || fw.perm.IsAutoApproveSession(sessionID)
	}
	return fw.yolo
}

// SessionState returns a snapshot built from the loop and checker. The fake has
// no file tracker, so ChangedFiles is always empty.
func (fw *fakeWorkspace) SessionState(_ context.Context, sessionID string) (app.SessionState, error) {
	st := app.SessionState{SessionID: sessionID, Yolo: fw.SessionYolo(sessionID)}
	if fw.loop != nil {
		st.Model = fw.loop.ActiveModel()
		if p := fw.loop.Provider(); p != nil {
			st.Provider = p.Name()
		}
	}
	return st, nil
}

// CreateSession delegates to the session repo.
func (fw *fakeWorkspace) CreateSession(ctx context.Context, s *session.Session) error {
	if fw.sessions == nil {
		return nil
	}
	return fw.sessions.Create(ctx, s)
}

// GetSession delegates to the session repo.
func (fw *fakeWorkspace) GetSession(ctx context.Context, id string) (*session.Session, error) {
	if fw.sessions == nil {
		return nil, nil
	}
	return fw.sessions.Get(ctx, id)
}

// ListSessions delegates to the session repo.
func (fw *fakeWorkspace) ListSessions(ctx context.Context, f session.ListFilter) ([]session.Session, error) {
	if fw.sessions == nil {
		return nil, nil
	}
	return fw.sessions.List(ctx, f)
}

// SetSessionTitle delegates to the session repo.
func (fw *fakeWorkspace) SetSessionTitle(ctx context.Context, id, title string) error {
	if fw.sessions == nil {
		return nil
	}
	return fw.sessions.SetTitle(ctx, id, title)
}

// Messages delegates to the session repo.
func (fw *fakeWorkspace) Messages(ctx context.Context, sessionID string) ([]message.Message, error) {
	if fw.sessions == nil {
		return nil, nil
	}
	return fw.sessions.Messages(ctx, sessionID)
}

// AppendMessage delegates to the session repo.
func (fw *fakeWorkspace) AppendMessage(ctx context.Context, sessionID string, msg message.Message) error {
	if fw.sessions == nil {
		return nil
	}
	return fw.sessions.AppendMessage(ctx, sessionID, msg)
}

// CurrentModel returns the loop's active model id, or "" when no loop is wired.
func (fw *fakeWorkspace) CurrentModel() string {
	if fw.loop == nil {
		return ""
	}
	return fw.loop.ActiveModel()
}

// CurrentProvider returns the loop's bound provider, or nil when no loop is wired.
func (fw *fakeWorkspace) CurrentProvider() llm.Provider {
	if fw.loop == nil {
		return nil
	}
	return fw.loop.Provider()
}

// Cwd returns "" — the fake is not scoped to a working directory.
func (fw *fakeWorkspace) Cwd() string { return "" }
