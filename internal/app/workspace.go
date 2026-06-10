// Package app wires BharatCode services into one dependency graph.
package app

import (
	"context"

	"github.com/arbazkhan971/bharatcode/internal/agent"
	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/arbazkhan971/bharatcode/internal/session"
)

// Workspace is the narrow seam the interactive UI depends on instead of the
// concrete App and its service fields. It exposes exactly three things: the one
// consolidated event stream to subscribe to, the handful of agent/permission
// operations a turn drives, and read-only views of the session store and the
// current run state (model, provider, working directory, yolo).
//
// The point of the seam is dependency inversion: the UI is written against this
// interface, so it can be exercised against a hand-written fake without standing
// up the full App graph, and the App stays free to refactor its internals as
// long as the surface below is preserved. It is deliberately additive — App
// keeps its exported fields, and this interface is implemented over them — so
// introducing it changes nothing for existing callers.
type Workspace interface {
	// Subscribe registers a subscriber on the consolidated UI event stream and
	// returns a receive-only channel plus a cancel func. This is the single
	// source the UI listens to: agent-loop transitions, permission requests, and
	// out-of-band notices all arrive here as UIEvents, so the UI no longer
	// subscribes to the agent and permission topics separately.
	Subscribe() (<-chan UIEvent, func())

	// Prompt drives one user turn to completion against sessionID, blocking until
	// the agent loop returns. It is the UI's "send prompt" entry point and mirrors
	// the underlying loop's Run: the caller supplies the user message, and run
	// progress is observed on the Subscribe stream rather than this call's return.
	Prompt(ctx context.Context, sessionID string, userMsg message.Message) error

	// Steer queues text as a steering message for an in-flight turn, returning
	// true when a turn was live and the text was queued onto it, and false when no
	// turn was active (the caller should then start a fresh Prompt). It lets the
	// user course-correct mid-turn without restarting.
	Steer(text string) (queued bool)

	// Interrupt cancels the in-flight turn, if any. It is safe to call when no
	// turn is running.
	Interrupt()

	// GrantPermission answers a pending permission request with approval, sending
	// the decision on the request's Reply channel so the blocked agent proceeds.
	// remember asks the checker to persist the decision for the session. It is the
	// "grant" half of the request/response handshake the Subscribe stream carries.
	GrantPermission(req pubsub.PermissionRequest, remember bool)

	// DenyPermission answers a pending permission request with denial, sending the
	// decision (and an optional reason shown to the agent) on the request's Reply
	// channel. It is the "deny" half of the permission handshake.
	DenyPermission(req pubsub.PermissionRequest, reason string)

	// SetYolo toggles global auto-approval, and Yolo reports its current state, so
	// the UI can flip and render the yolo affordance through the seam rather than
	// reaching into the permission checker.
	SetYolo(on bool)
	Yolo() bool

	// CreateSession persists a new session record.
	CreateSession(ctx context.Context, s *session.Session) error
	// GetSession returns the session with id, or an error when it does not exist.
	GetSession(ctx context.Context, id string) (*session.Session, error)
	// ListSessions returns sessions matching the filter, newest first.
	ListSessions(ctx context.Context, f session.ListFilter) ([]session.Session, error)
	// SetSessionTitle renames a session.
	SetSessionTitle(ctx context.Context, id, title string) error
	// Messages returns the full message history for a session in order.
	Messages(ctx context.Context, sessionID string) ([]message.Message, error)
	// AppendMessage appends one message to a session's history.
	AppendMessage(ctx context.Context, sessionID string, msg message.Message) error

	// CurrentModel returns the model id the active agent is bound to, reflecting
	// the latest model switch.
	CurrentModel() string
	// CurrentProvider returns the provider the active agent is bound to.
	CurrentProvider() llm.Provider
	// Cwd returns the resolved, absolute working directory the App is scoped to.
	Cwd() string
}

// appWorkspace implements Workspace over an App and the live agent Loop that
// serves the interactive session. The Loop is the one resolved at UI startup
// (the "coder" agent); holding it here lets the run-state accessors and the
// prompt/steer/interrupt operations target the same Loop the UI drives, while
// the session, permission, and event-stream operations delegate to the App.
type appWorkspace struct {
	app  *App
	loop *agent.Loop
	// runner enforces per-session run discipline over the live loop: one active
	// run per session, additional prompts queued in order, and atomic cancel. The
	// seam drives Prompt through it so a second prompt submitted mid-turn is
	// queued behind the first rather than racing the Loop (which rejects
	// concurrent runs).
	runner *agent.SessionRunner
}

// NewWorkspace returns a Workspace backed by app and the live loop that serves
// the interactive session (typically app.Agent.Agent("coder")). Both must be
// non-nil; the UI seam is meaningless without a stream to subscribe to and a
// loop to drive. It performs no wiring of its own — it is a thin adapter over
// the already-constructed graph.
func NewWorkspace(app *App, loop *agent.Loop) Workspace {
	return &appWorkspace{app: app, loop: loop, runner: agent.NewSessionRunner(loop.Run)}
}

// Subscribe delegates to the consolidated UI stream the App fanned in at New.
func (w *appWorkspace) Subscribe() (<-chan UIEvent, func()) {
	return w.app.UI.Subscribe()
}

// Prompt submits the turn through the per-session runner and blocks until it
// completes, preserving the direct-Run semantics callers expect while gaining
// run discipline: if a turn is already active for sessionID, this prompt is
// queued behind it and runs once the active turn (and anything queued ahead)
// finishes, instead of racing the Loop.
func (w *appWorkspace) Prompt(ctx context.Context, sessionID string, userMsg message.Message) error {
	return w.runner.Submit(ctx, sessionID, userMsg).Wait()
}

// Steer forwards to the live loop's Steer.
func (w *appWorkspace) Steer(text string) (queued bool) {
	return w.loop.Steer(text)
}

// Interrupt cancels the in-flight turn and drops any queued prompts. It cancels
// the live loop's active run directly (interrupting the provider stream) and
// clears the runner's per-session queues so no deferred prompt fires after an
// interrupt. It is safe to call when nothing is running.
func (w *appWorkspace) Interrupt() {
	w.loop.Interrupt()
	w.runner.CancelAll()
}

// GrantPermission sends an approving decision on the request's Reply channel.
// The Reply channel is buffered with capacity 1 (the producer's contract), so
// the send never blocks even if the agent has not yet reached its <-Reply; a nil
// Reply (a malformed request) is skipped rather than panicking.
func (w *appWorkspace) GrantPermission(req pubsub.PermissionRequest, remember bool) {
	if req.Reply == nil {
		return
	}
	req.Reply <- pubsub.PermissionDecision{Approved: true, Remember: remember}
}

// DenyPermission sends a denying decision (with an optional reason) on the
// request's Reply channel, following the same capacity-1, nil-safe contract as
// GrantPermission.
func (w *appWorkspace) DenyPermission(req pubsub.PermissionRequest, reason string) {
	if req.Reply == nil {
		return
	}
	req.Reply <- pubsub.PermissionDecision{Approved: false, Reason: reason}
}

// SetYolo toggles auto-approval on the permission checker.
func (w *appWorkspace) SetYolo(on bool) {
	w.app.Permission.SetYolo(on)
}

// Yolo reports the permission checker's current yolo state.
func (w *appWorkspace) Yolo() bool {
	return w.app.Permission.Yolo()
}

// CreateSession delegates to the session repository.
func (w *appWorkspace) CreateSession(ctx context.Context, s *session.Session) error {
	return w.app.Sessions.Create(ctx, s)
}

// GetSession delegates to the session repository.
func (w *appWorkspace) GetSession(ctx context.Context, id string) (*session.Session, error) {
	return w.app.Sessions.Get(ctx, id)
}

// ListSessions delegates to the session repository.
func (w *appWorkspace) ListSessions(ctx context.Context, f session.ListFilter) ([]session.Session, error) {
	return w.app.Sessions.List(ctx, f)
}

// SetSessionTitle delegates to the session repository.
func (w *appWorkspace) SetSessionTitle(ctx context.Context, id, title string) error {
	return w.app.Sessions.SetTitle(ctx, id, title)
}

// Messages delegates to the session repository.
func (w *appWorkspace) Messages(ctx context.Context, sessionID string) ([]message.Message, error) {
	return w.app.Sessions.Messages(ctx, sessionID)
}

// AppendMessage delegates to the session repository.
func (w *appWorkspace) AppendMessage(ctx context.Context, sessionID string, msg message.Message) error {
	return w.app.Sessions.AppendMessage(ctx, sessionID, msg)
}

// CurrentModel returns the live loop's active model id.
func (w *appWorkspace) CurrentModel() string {
	return w.loop.ActiveModel()
}

// CurrentProvider returns the live loop's bound provider.
func (w *appWorkspace) CurrentProvider() llm.Provider {
	return w.loop.Provider()
}

// Cwd returns the App's resolved working directory.
func (w *appWorkspace) Cwd() string {
	return w.app.WorkDir()
}
