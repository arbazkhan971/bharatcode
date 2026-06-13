// Package app wires BharatCode services into one dependency graph.
package app

import (
	"context"
	"fmt"
	"sync"

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
//
// Per-session operations (Prompt, Steer, Compact, SetModel, plan mode, Approve,
// PendingSteering, the per-session Interrupt, and ReleaseSession) take a
// sessionID and route to that session's own Loop instance, so distinct sessions
// run concurrently without sharing mutable Loop state.
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

	// Steer queues text as a steering message for sessionID's in-flight turn,
	// returning true when a turn was live and the text was queued onto it, and
	// false when no turn was active (the caller should then start a fresh Prompt).
	// It lets the user course-correct mid-turn without restarting.
	Steer(sessionID, text string) (queued bool)

	// Interrupt cancels every in-flight turn across all sessions, if any. It is
	// the global Ctrl-C affordance and is safe to call when nothing is running.
	Interrupt()

	// InterruptSession cancels only sessionID's in-flight turn (and drops its
	// queued prompts), leaving other sessions running. It is the per-tab interrupt
	// path and is safe to call when nothing is running for sessionID.
	InterruptSession(sessionID string)

	// Compact condenses sessionID's conversation in memory so the next provider
	// request for it sends a smaller history.
	Compact(ctx context.Context, sessionID string) error

	// SetModel rebinds sessionID's Loop to a different model and provider.
	SetModel(sessionID, model string, provider llm.Provider)

	// PlanMode reports whether sessionID's Loop is currently in plan mode, and
	// SetPlanMode turns it on or off; Approve exits plan mode (equivalent to
	// SetPlanMode(false)) for sessionID.
	PlanMode(sessionID string) bool
	SetPlanMode(sessionID string, on bool)
	Approve(sessionID string)

	// PendingSteering drains and returns sessionID's queued-but-unsent steering
	// messages so a finished turn can restart leftover text as a fresh prompt.
	PendingSteering(sessionID string) []string

	// ApprovePlan exits plan mode on sessionID's Loop and returns the stored
	// plan text so the caller can seed the next execution turn with it. It routes
	// the Coordinator's plan approval through that session's own Loop.
	ApprovePlan(sessionID string) string

	// ReleaseSession drops sessionID's cached Loop and runner bookkeeping once a
	// tab closes, so a long-lived process does not accumulate per-session state.
	// It is safe to call for a never-seen session.
	ReleaseSession(sessionID string)

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
	// the sidebar to render. It reads through to the session's loop, the
	// permission checker, and the file tracker, so it always reflects the current
	// run.
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

	// CurrentModel returns the model id the default agent is bound to, reflecting
	// the latest model switch. It reads the default loop for the global status
	// line and does not mint a per-session Loop.
	CurrentModel() string
	// CurrentProvider returns the provider the default agent is bound to.
	CurrentProvider() llm.Provider
	// Cwd returns the resolved, absolute working directory the App is scoped to.
	Cwd() string
}

// appWorkspace implements Workspace over an App and a per-session cache of agent
// Loops. Each session resolves to its OWN Loop instance (minted via the
// Coordinator's Agent factory and cached under loopMu), so distinct sessions
// never share mutable Loop state and can run concurrently. The original loop —
// the one resolved at UI startup (the "coder" agent) — is retained as the
// default/fallback for global status reads (CurrentModel/CurrentProvider) and
// when minting a fresh per-session Loop fails.
type appWorkspace struct {
	app  *App
	loop *agent.Loop
	// runner enforces per-session run discipline: one active run per session,
	// additional prompts queued in order, and atomic cancel. Each session's queue
	// has its own execution mutex, so distinct sessions run in parallel; the seam
	// drives Prompt through it via a closure that resolves the session's Loop.
	runner *agent.SessionRunner

	// loopMu guards loops. loops caches one Loop per sessionID so every operation
	// for a given session targets the same instance (preserving its compaction
	// and steering state and per-session FIFO ordering). Concurrent first-Prompts
	// for distinct sessions mint distinct Loops; the double-check under loopMu
	// guarantees one session never mints two Loops.
	loopMu sync.Mutex
	loops  map[string]*agent.Loop
}

// NewWorkspace returns a Workspace backed by app and the live loop that serves
// the interactive session (typically app.Agent.Agent("coder")). Both must be
// non-nil; the UI seam is meaningless without a stream to subscribe to and a
// loop to drive. The runner is built from a closure that resolves each session's
// own Loop (so distinct sessions run concurrently), which is why the struct is
// constructed first and its runner assigned after — the closure captures w.
func NewWorkspace(app *App, loop *agent.Loop) Workspace {
	w := &appWorkspace{
		app:   app,
		loop:  loop,
		loops: make(map[string]*agent.Loop),
	}
	w.runner = agent.NewSessionRunner(func(ctx context.Context, sessionID string, msg message.Message) error {
		return w.loopFor(sessionID).Run(ctx, sessionID, msg)
	})
	return w
}

// loopFor returns the Loop dedicated to sessionID, minting and caching one via
// the Coordinator's Agent factory on first use. The whole resolve-or-mint runs
// under loopMu (a double-check) so two concurrent first-Prompts for one session
// can never split it across two Loops, which would fork that session's
// compaction and steering state and break its FIFO ordering. When minting fails
// (or no Coordinator is wired), it falls back to the default loop so behaviour
// degrades gracefully rather than panicking.
func (w *appWorkspace) loopFor(sessionID string) *agent.Loop {
	w.loopMu.Lock()
	defer w.loopMu.Unlock()
	if l, ok := w.loops[sessionID]; ok {
		return l
	}
	if w.app == nil || w.app.Agent == nil {
		return w.loop
	}
	l, err := w.app.Agent.Agent("coder")
	if err != nil || l == nil {
		return w.loop
	}
	w.loops[sessionID] = l
	return l
}

// Subscribe delegates to the consolidated UI stream the App fanned in at New.
func (w *appWorkspace) Subscribe() (<-chan UIEvent, func()) {
	return w.app.UI.Subscribe()
}

// Prompt submits the turn through the per-session runner and blocks until it
// completes, preserving the direct-Run semantics callers expect while gaining
// run discipline: if a turn is already active for sessionID, this prompt is
// queued behind it and runs once the active turn (and anything queued ahead)
// finishes. Distinct sessions resolve distinct Loops and run concurrently.
func (w *appWorkspace) Prompt(ctx context.Context, sessionID string, userMsg message.Message) error {
	return w.runner.Submit(ctx, sessionID, userMsg).Wait()
}

// Steer forwards to sessionID's loop's Steer.
func (w *appWorkspace) Steer(sessionID, text string) (queued bool) {
	return w.loopFor(sessionID).Steer(text)
}

// Interrupt cancels every in-flight turn across all sessions and drops every
// queued prompt. It interrupts each cached Loop's active run directly (cancelling
// the provider stream) and clears the runner's per-session queues, so the single
// Ctrl-C affordance still stops everything. It is safe to call when nothing is
// running.
func (w *appWorkspace) Interrupt() {
	w.loopMu.Lock()
	for _, l := range w.loops {
		l.Interrupt()
	}
	w.loopMu.Unlock()
	w.loop.Interrupt()
	w.runner.CancelAll()
}

// InterruptSession cancels only sessionID's in-flight turn and drops its queued
// prompts, leaving other sessions running. It is the per-tab interrupt path.
func (w *appWorkspace) InterruptSession(sessionID string) {
	w.loopFor(sessionID).Interrupt()
	w.runner.Cancel(sessionID)
}

// Compact condenses sessionID's conversation through its own loop.
func (w *appWorkspace) Compact(ctx context.Context, sessionID string) error {
	return w.loopFor(sessionID).Compact(ctx, sessionID)
}

// SetModel rebinds sessionID's loop to a different model and provider.
func (w *appWorkspace) SetModel(sessionID, model string, provider llm.Provider) {
	w.loopFor(sessionID).SetModel(model, provider)
}

// PlanMode reports whether sessionID's loop is in plan mode.
func (w *appWorkspace) PlanMode(sessionID string) bool {
	return w.loopFor(sessionID).PlanMode()
}

// SetPlanMode toggles plan mode on sessionID's loop.
func (w *appWorkspace) SetPlanMode(sessionID string, on bool) {
	w.loopFor(sessionID).SetPlanMode(on)
}

// Approve exits plan mode on sessionID's loop.
func (w *appWorkspace) Approve(sessionID string) {
	w.loopFor(sessionID).Approve()
}

// PendingSteering drains sessionID's queued steering messages.
func (w *appWorkspace) PendingSteering(sessionID string) []string {
	return w.loopFor(sessionID).PendingSteering()
}

// ApprovePlan exits plan mode on sessionID's Loop and returns the stored plan
// text, routing the Coordinator's plan approval through that session's own
// Loop. When no Coordinator is wired it falls back to clearing plan mode on the
// session's Loop and returns an empty plan.
func (w *appWorkspace) ApprovePlan(sessionID string) string {
	loop := w.loopFor(sessionID)
	if w.app == nil || w.app.Agent == nil {
		loop.Approve()
		return ""
	}
	return w.app.Agent.ApprovePlan(sessionID, loop)
}

// ReleaseSession drops sessionID's cached Loop and runner queue. It cancels any
// live work first so a released session leaves nothing draining, then deletes
// the cache entry under loopMu and removes the (now idle) runner queue. Loops
// hold no OS resources, so deletion suffices for GC. Safe for a never-seen
// session.
func (w *appWorkspace) ReleaseSession(sessionID string) {
	w.runner.Cancel(sessionID)
	w.loopMu.Lock()
	delete(w.loops, sessionID)
	w.loopMu.Unlock()
	w.runner.Remove(sessionID)
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

// SessionState assembles the live snapshot for the sidebar from the session's
// loop (model, provider), the App (working directory), the permission checker
// (yolo), and the file tracker (changed files). A nil file tracker or empty
// sessionID yields an empty ChangedFiles set rather than an error.
func (w *appWorkspace) SessionState(ctx context.Context, sessionID string) (SessionState, error) {
	loop := w.loopFor(sessionID)
	st := SessionState{
		SessionID: sessionID,
		Model:     loop.ActiveModel(),
		Cwd:       w.app.WorkDir(),
		Yolo:      w.SessionYolo(sessionID),
	}
	if p := loop.Provider(); p != nil {
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

// CurrentModel returns the default loop's active model id for the global status
// line. It reads the default loop rather than minting a per-session Loop, since
// this is a status read, not per-turn state.
func (w *appWorkspace) CurrentModel() string {
	return w.loop.ActiveModel()
}

// CurrentProvider returns the default loop's bound provider for the global
// status line.
func (w *appWorkspace) CurrentProvider() llm.Provider {
	return w.loop.Provider()
}

// Cwd returns the App's resolved working directory.
func (w *appWorkspace) Cwd() string {
	return w.app.WorkDir()
}
