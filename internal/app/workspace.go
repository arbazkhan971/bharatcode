// Package app wires BharatCode services into one dependency graph.
package app

import (
	"context"
	"fmt"

	"github.com/arbazkhan971/bharatcode/internal/agent"
	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/arbazkhan971/bharatcode/internal/session"
)

// SessionState is a live snapshot of a session's user-visible run state, surfaced
// through the Workspace seam for the sidebar. It bundles the fields the sidebar
// shows — the active model and provider, the working directory, whether the
// session is auto-approving (yolo), and the absolute paths changed during the
// session — so the UI reads one consistent view instead of polling several
// services. ChangedFiles is sorted and deduplicated (see filetracker.ChangedFiles).
type SessionState struct {
	SessionID    string
	Model        string
	Provider     string
	Cwd          string
	Yolo         bool
	ChangedFiles []string
}

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

	// SetSessionYolo toggles per-session auto-approval for sessionID, and
	// SessionYolo reports it. This is the session-scoped form of yolo: --yolo and
	// the in-UI yolo toggle flip approval for the active session only, so one
	// session can run unattended while another keeps prompting.
	SetSessionYolo(sessionID string, on bool)
	SessionYolo(sessionID string) bool

	// SessionState returns a live snapshot of the session's user-visible state —
	// model, provider, working directory, yolo, and the files changed so far — for
	// the sidebar to render. It reads through to the live loop, the permission
	// checker, and the file tracker, so it always reflects the current run.
	SessionState(ctx context.Context, sessionID string) (SessionState, error)

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
}

// NewWorkspace returns a Workspace backed by app and the live loop that serves
// the interactive session (typically app.Agent.Agent("coder")). Both must be
// non-nil; the UI seam is meaningless without a stream to subscribe to and a
// loop to drive. It performs no wiring of its own — it is a thin adapter over
// the already-constructed graph.
func NewWorkspace(app *App, loop *agent.Loop) Workspace {
	return &appWorkspace{app: app, loop: loop}
}

// Subscribe delegates to the consolidated UI stream the App fanned in at New.
func (w *appWorkspace) Subscribe() (<-chan UIEvent, func()) {
	return w.app.UI.Subscribe()
}

// Prompt forwards to the live loop's Run.
func (w *appWorkspace) Prompt(ctx context.Context, sessionID string, userMsg message.Message) error {
	return w.loop.Run(ctx, sessionID, userMsg)
}

// Steer forwards to the live loop's Steer.
func (w *appWorkspace) Steer(text string) (queued bool) {
	return w.loop.Steer(text)
}

// Interrupt forwards to the live loop's Interrupt.
func (w *appWorkspace) Interrupt() {
	w.loop.Interrupt()
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

// SetSessionYolo toggles per-session auto-approval through the checker.
func (w *appWorkspace) SetSessionYolo(sessionID string, on bool) {
	w.app.Permission.SetAutoApproveSession(sessionID, on)
}

// SessionYolo reports whether sessionID is auto-approving. It is the per-session
// companion to Yolo and treats global yolo as covering every session, so the
// sidebar shows yolo when either the global switch or this session's grant is on.
func (w *appWorkspace) SessionYolo(sessionID string) bool {
	return w.app.Permission.Yolo() || w.app.Permission.IsAutoApproveSession(sessionID)
}

// SessionState assembles the live snapshot for the sidebar from the loop (model,
// provider), the App (working directory), the permission checker (yolo), and the
// file tracker (changed files). A nil file tracker or empty sessionID yields an
// empty ChangedFiles set rather than an error.
func (w *appWorkspace) SessionState(ctx context.Context, sessionID string) (SessionState, error) {
	st := SessionState{
		SessionID: sessionID,
		Model:     w.loop.ActiveModel(),
		Cwd:       w.app.WorkDir(),
		Yolo:      w.SessionYolo(sessionID),
	}
	if p := w.loop.Provider(); p != nil {
		st.Provider = p.Name()
	}
	if w.app.FileTracker != nil && sessionID != "" {
		files, err := w.app.FileTracker.ChangedFiles(ctx, sessionID)
		if err != nil {
			return st, fmt.Errorf("collecting changed files for session %s: %w", sessionID, err)
		}
		st.ChangedFiles = files
	}
	return st, nil
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
