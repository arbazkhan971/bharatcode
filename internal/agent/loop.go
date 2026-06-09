package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/filetracker"
	"github.com/arbazkhan971/bharatcode/internal/hooks"
	"github.com/arbazkhan971/bharatcode/internal/ledger"
	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/permission"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/arbazkhan971/bharatcode/internal/session"
	"github.com/arbazkhan971/bharatcode/internal/tools"
)

const defaultMaxSteps = 50

// defaultMaxVerifyAttempts caps how many times a single Run will run the
// policy-driven verification command and feed a failure back to the model. The
// cap exists purely to stop a model that cannot fix the failure from looping the
// verify→fix→verify cycle forever; once it is hit the loop stops re-verifying
// and lets the turn proceed so the model can explain the unresolved failure
// rather than the run hanging. Per-file hook verifiers (runVerifyCommands) are
// not bounded by this: they always run because they are user-declared.
const defaultMaxVerifyAttempts = 3

// planModePrompt is appended to the system prompt while the Loop runs in plan
// mode. It frames a phased, read-only investigation-then-design workflow as a
// system reminder and instructs the agent to stop for approval instead of
// executing changes, mirroring the read-only tool restriction enforced by
// toolAllowed (the runtime guard) and the Approve exit affordance.
const planModePrompt = `

<system-reminder>
Plan mode is active. While it is active you MUST NOT make any edits, run any
non-readonly tools (including changing configs or making commits), or mutate the
workspace in any way. This supersedes any other instructions you have received,
including a direct request from the user to act before the plan is approved.
Only read-only tools are available; everything else will be refused.

Your job in plan mode is to investigate, think hard, and hand back a precise,
approvable plan. Move through these phases in order:

Phase 1 - Investigate (read-only). Do focused exploration yourself: read the
relevant files, search the codebase, and trace how the affected pieces fit
together. There is no subagent to delegate to, so gather the context directly
and confirm assumptions against the actual code rather than guessing.

Phase 2 - Clarify. Surface any open questions, ambiguities, or decisions that
materially change the approach, and resolve them BEFORE you start designing. If
something genuinely blocks a sound plan, ask the user; do not paper over it.

Phase 3 - Design. Decide on the approach. Weigh the realistic alternatives,
pick one, and be explicit about the trade-offs and why this option wins.

Phase 4 - Review. Re-read the critical files your design touches and re-check
that the approach still lines up with how the code actually behaves. Correct the
design if the review turns up anything that no longer holds.

Phase 5 - Write the plan, then STOP. Produce a clear, ordered, step-by-step plan
the user can approve. Reference concrete files and symbols, keep each step
actionable, and end with a Verification section stating exactly how the change
will be checked (build, tests, and any manual checks). After presenting the
plan, stop and wait for approval; do not begin executing it. Execution only
starts once the user approves and plan mode is cleared.
</system-reminder>`

// maxStepsPrompt is appended to the system prompt for the single, tools-disabled
// final turn the Loop grants once it reaches the configured step limit. Instead
// of truncating the turn with a dead-end "step limit reached" line, the Loop
// gives the model one more turn with tools removed and these instructions so it
// produces a useful handoff: what was accomplished, what remains, and what to do
// next. The wording deliberately overrides any earlier instruction to keep
// calling tools, since none are available on this turn. This is BharatCode's own
// adaptation of the max-steps handoff pattern.
const maxStepsPrompt = `

# Maximum steps reached

The per-turn step limit has been reached, so every tool has been removed for this
final turn. You cannot call tools and must reply with plain text only. This
instruction takes priority over any earlier guidance that told you to keep using
tools or to continue working.

Write a concise progress handoff that the next session can pick up from. It must
contain, in order:

1. A clear statement that the step limit was reached and work paused here.
2. A summary of what you accomplished during this turn.
3. The tasks that still remain to be done.
4. Your recommendations for the next steps to take.

Do not attempt any further actions; just deliver this handoff as your reply.`

// goalFrameHeader and goalFrameFooter wrap the user's original request when the
// Loop re-injects it as an "active goal" frame on every provider call. Anchoring
// the goal in the system prompt — outside the conversation history — keeps the
// agent pointed at what the user actually asked for even after dozens of steps
// and after compaction has dropped the opening messages from history. This is
// BharatCode's persistent goal frame.
const goalFrameHeader = `

<system-reminder>
Active goal for this session — the user's original request. Keep it in focus and
make sure your work continues to serve it, even as the conversation grows and
earlier messages scroll out of context:

`

const goalFrameFooter = `
</system-reminder>`

// maxGoalFrameRunes caps the length of the re-injected goal so a very long
// opening message cannot bloat every system prompt for the rest of the session.
// Beyond this the goal is truncated with an ellipsis; the full request still
// lives in the conversation history.
const maxGoalFrameRunes = 1500

// writeClassTools names the file-mutating tools whose successful runs fire a
// FileEdit lifecycle hook. The agent loop detects mutations by tool name so it
// stays decoupled from the tools package's concrete implementations.
var writeClassTools = map[string]struct{}{
	"write":     {},
	"edit":      {},
	"multiedit": {},
}

// hookFirer is the subset of *hooks.Engine the agent loop consumes to fire
// lifecycle hooks. It is an interface so tests can inject a capturing fake via
// the Config; *hooks.Engine satisfies it. A nil hookFirer means no hooks are
// configured and every Fire call is skipped.
type hookFirer interface {
	Fire(ctx context.Context, event hooks.Event, payload any) (hooks.Decision, error)
}

// verifyHookSource is the subset of *hooks.Engine that provides verify
// commands for edited files. It is an interface so tests can inject a fake;
// *hooks.Engine satisfies it. A nil verifyHookSource means no verify commands
// are configured and verification is always skipped.
type verifyHookSource interface {
	MatchingVerifiers(filePath string) []hooks.VerifySpec
}

// verifyRunner executes a single verify command in a subprocess and returns
// its combined stdout+stderr output. A non-zero exit code is returned as an
// error so the caller can distinguish success from verification failure.
// It is an interface so tests can inject a deterministic fake.
type verifyRunner interface {
	RunVerify(ctx context.Context, command, cwd string, timeout time.Duration) (output string, err error)
}

// execVerifyRunner is the default verifyRunner that runs commands through
// /bin/sh -c, mirroring how the hooks engine executes user commands.
type execVerifyRunner struct{}

func (execVerifyRunner) RunVerify(ctx context.Context, command, cwd string, timeout time.Duration) (string, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command) //nolint:gosec
	if cwd != "" {
		cmd.Dir = cwd
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		// Return output alongside the error so the caller can include it in
		// the tool result shown to the model.
		return out.String(), err
	}
	return out.String(), nil
}

// Config bundles the dependencies a Loop needs.
type Config struct {
	Name          string
	Model         string
	Provider      llm.Provider
	Tools         toolSource
	Permission    *permission.Checker
	Sessions      *session.Repo
	FileTracker   *filetracker.Tracker
	Ledger        *ledger.Ledger
	Bus           *pubsub.Topic[Event]
	Hooks         hookFirer
	SystemPrompt  string
	ToolAllowList []string
	MaxSteps      int
	// PlanMode starts the Loop in plan mode: the agent is restricted to
	// read-only tools and is prompted to output a step-by-step plan rather than
	// execute changes. Approve transitions out of plan mode so execution tools
	// become available again. It defaults to false (normal execution), which is
	// the non-breaking default.
	PlanMode bool
	// ToolResultMaxBytes caps the byte length of a single tool result before it
	// is appended to the conversation history, so one oversized output (a giant
	// file read, verbose bash output) cannot blow the context window. A
	// non-positive value selects defaultToolResultMaxBytes. Error results are
	// never truncated.
	ToolResultMaxBytes int
	// Compactor condenses the conversation before it is sent to the provider
	// when Compact is invoked. When nil, a default drop-and-mark Compactor is
	// used.
	Compactor Compactor
	// Router selects which configured model to use for each turn, enabling
	// cost-aware routing of cheap models to simple turns and stronger models to
	// complex ones. When nil, no routing occurs and the Loop always uses Model,
	// which is the default, non-breaking behavior.
	Router Router
	// RouteHint is an explicit complexity override forwarded to Router on every
	// turn this Loop runs. It is set once at construction (a Loop serves many
	// Run calls), so it pins the same hint for the Loop's lifetime rather than
	// varying per turn. It defaults to ComplexityUnset, leaving the Router to
	// derive complexity from each turn. It is ignored when Router is nil.
	RouteHint TurnComplexity
	// VerifyHooks supplies verify commands that run after a successful
	// write-class tool execution. When nil, verification is disabled and the
	// loop behaves exactly as before. *hooks.Engine satisfies this interface;
	// a test fake may be injected instead.
	VerifyHooks verifyHookSource
	// VerifyRunner executes verify commands. When nil it defaults to the
	// execVerifyRunner, which runs commands through /bin/sh -c. Tests may
	// inject a deterministic fake to control verify outcomes without forking
	// real subprocesses.
	VerifyRunner verifyRunner
	// Verification carries BharatCode's verification policy: which change
	// classes oblige the agent to verify before reporting work done. The zero
	// value (an omitted "verification" block) selects the strict default — every
	// write-class edit requires verification — so post-write verification is on
	// by default. Set Disabled in the policy to make it advisory only. The policy
	// only takes effect when a verify command can be discovered for the workspace
	// (see WorkDir); when none is found the edit is left to the per-file hook
	// verifiers or to the model.
	Verification config.VerificationConfig
	// WorkDir is the repo root scanned by DiscoverVerifyCommands to find the
	// command that verifies a change (go test ./..., npm run build, …). When
	// empty it defaults to the current directory. It is also the working
	// directory the discovered command runs in.
	WorkDir string
	// MaxVerifyAttempts caps how many policy-driven verify→fix cycles a single
	// Run performs before it stops re-verifying and lets the model explain an
	// unresolved failure. A non-positive value selects defaultMaxVerifyAttempts.
	// It does not bound user-declared per-file hook verifiers.
	MaxVerifyAttempts int
	// ToolAuditor, when set, records every tool invocation in the append-only
	// audit log. It is nil by default, leaving tool auditing off — the
	// non-breaking default. The app wires an audit-backed logger here so the
	// sovereignty proof layer captures the tool calls the agent ran, not just
	// the permission decisions it was granted.
	ToolAuditor ToolAuditor
	// LLMAuditor, when set, records every model-provider turn in the append-only
	// audit log. It is nil by default, leaving LLM auditing off — the
	// non-breaking default. The app wires an audit-backed logger here so the
	// sovereignty proof layer captures the egress to the model: which provider
	// and model the prompt was sent to, alongside the local tool calls and
	// permission decisions already recorded.
	LLMAuditor LLMAuditor
	// AutoCompactThreshold, when positive, enables automatic context
	// compaction. After each provider turn the loop checks whether the
	// input-token count divided by the model's context window exceeds this
	// fraction. When it does, the loop compacts the in-memory history so the
	// next turn sees a smaller, summarised conversation instead of silently
	// overflowing. A value of 0 (the default) disables auto-compaction.
	// Typical values are 0.85–0.95. Values ≥ 1.0 are treated as disabled.
	AutoCompactThreshold float64
}

// Loop runs a single named agent for one session at a time.
type Loop struct {
	cfg       Config
	name      string
	runMu     sync.Mutex
	cancelMu  sync.Mutex
	cancelRun context.CancelFunc
	allowed   map[string]struct{}

	// modelMu guards cfg.Model, cfg.Provider, and activeModel when they are
	// read or written by SetModel/Provider outside of a Run. Using a separate
	// RWMutex for these fields means SetModel/Provider never contend with the
	// long-held runMu, so calling Provider() from the TUI while a turn is in
	// flight (e.g. for status-bar rendering) cannot deadlock.
	modelMu sync.RWMutex

	// compactMu guards the in-memory compaction snapshot below. Run reads it
	// and Compact writes it, potentially from different goroutines.
	compactMu sync.Mutex
	// compacted holds the condensed history produced by the most recent
	// Compact call. It is nil when no compaction has occurred. It lives only in
	// memory and is never written to the on-disk session.
	compacted []message.Message
	// compactedLen records the on-disk message count at compaction time, so Run
	// can graft messages that arrived after compaction onto the snapshot.
	compactedLen int

	// steerMu guards the steering queue below. Steer writes it (possibly from a
	// different goroutine than Run), and Run drains it at safe boundaries.
	steerMu sync.Mutex
	// steerQueue holds user steering messages queued mid-run via Steer. Run
	// drains them at the top of each step so they reach the provider as the next
	// user messages without restarting the turn.
	steerQueue []string
	// running reports whether a Run is currently in flight. Steer reads it to
	// tell the caller whether the steering text was queued onto a live turn or
	// must be started as a fresh prompt.
	running bool

	// backoff drives the retry schedule for transient provider failures. It is
	// defaulted in New and overridden in-package by tests for determinism.
	backoff llm.Backoff
	// sleep waits between provider retries, honouring context cancellation.
	// Production uses contextSleep; tests inject a no-op recorder so retries do
	// not sleep for real.
	sleep sleepFunc

	// activeModel is the model resolved for the current turn. It is set once at
	// the top of Run (from the configured Router, or cfg.Model when no Router is
	// set) and read by every per-step provider call so the whole turn uses one
	// model. Run holds runMu for its entire duration and rejects concurrent
	// runs, so no further synchronization is needed.
	activeModel string

	// verifyAttempts counts how many times the policy-driven verification
	// command has run in the current Run. It is reset at the top of Run and read
	// and written only on the (single-threaded) Run goroutine — Run holds runMu
	// for its whole duration and rejects concurrent runs — so it needs no
	// synchronization. It bounds the verify→fix→verify cycle at MaxVerifyAttempts.
	verifyAttempts int

	// planMode reports whether the Loop is currently restricted to read-only
	// tools and prompted to produce a plan. It is initialised from cfg.PlanMode
	// and cleared by Approve. It is atomic because Approve may be called from a
	// different goroutine than the in-flight Run that reads it.
	planMode atomic.Bool

	// finalTurn reports whether the current provider call is the tools-disabled
	// handoff turn granted once the step limit is reached. While set, llmTools
	// returns no tools and systemPrompt appends maxStepsPrompt, so the model is
	// forced to summarise its progress in plain text instead of calling more
	// tools. Run sets it only for the final step's call and clears it afterward.
	// It is atomic for the same reason planMode is: it is read by the provider
	// call path while a concurrent goroutine could observe the Loop.
	finalTurn atomic.Bool

	// goalMu guards goalFrameText.
	goalMu sync.Mutex
	// goalFrameText holds the user's original request for the session — the
	// first user message. Run captures it from the full on-disk history at the
	// top of every turn (before compaction, which only trims the in-memory
	// history) and systemPrompt re-injects it as the active-goal frame so the
	// agent stays anchored to what the user asked for even after the opening
	// messages have scrolled out of the conversation. It is read on the provider
	// call path and written by Run, so a mutex guards it.
	goalFrameText string

	// smallMu guards smallTaskPrompt.
	smallMu sync.Mutex
	// smallTaskPrompt holds the concise system prompt used when the turn is a
	// small task — a short file-generation request in an empty or near-empty
	// directory (see prompts.go). It is rendered once at the top of Run when the
	// detection fires and is empty otherwise; framedSystemPrompt substitutes it
	// for cfg.SystemPrompt so a simple "write me an X" turn does not pay for the
	// full coder doctrine. It is read on the provider call path and written by
	// Run, so a mutex guards it.
	smallTaskPrompt string
}

// New constructs a Loop from cfg.
func New(cfg Config) *Loop {
	if cfg.Name == "" {
		cfg.Name = "coder"
	}
	if cfg.MaxSteps <= 0 {
		cfg.MaxSteps = defaultMaxSteps
	}
	if cfg.Provider == nil {
		panic("agent: provider is nil")
	}
	if cfg.Tools == nil {
		panic("agent: tools registry is nil")
	}
	if cfg.Sessions == nil {
		panic("agent: sessions repo is nil")
	}

	allowed := make(map[string]struct{}, len(cfg.ToolAllowList))
	for _, name := range cfg.ToolAllowList {
		if _, ok := cfg.Tools.Get(name); !ok {
			panic("agent: allowed tool is not registered: " + name)
		}
		allowed[name] = struct{}{}
	}
	l := &Loop{
		cfg:         cfg,
		name:        cfg.Name,
		allowed:     allowed,
		backoff:     llm.Backoff{},
		sleep:       contextSleep,
		activeModel: cfg.Model,
	}
	l.planMode.Store(cfg.PlanMode)
	return l
}

// Name returns the configured agent name.
func (l *Loop) Name() string {
	return l.name
}

// PlanMode reports whether the Loop is currently restricted to read-only tools
// and prompted to produce a plan.
func (l *Loop) PlanMode() bool {
	return l.planMode.Load()
}

// SetPlanMode turns plan mode on or off at runtime. Turning it on restricts the
// next provider call to read-only tools and appends the plan-mode prompt;
// turning it off (equivalent to Approve) restores execution tools. It takes
// effect on the next provider call and is safe to call from any goroutine, so a
// UI can toggle plan mode on a live Loop without recreating it (which would lose
// session state). Approve remains the dedicated "exit plan mode" affordance.
func (l *Loop) SetPlanMode(on bool) {
	l.planMode.Store(on)
}

// Approve transitions the Loop out of plan mode so execution tools (write, edit,
// bash, and similar) become available again and the plan-mode prompt is no
// longer appended. It takes effect on the next provider call. Approve is safe to
// call from any goroutine; calling it when not in plan mode is a no-op.
func (l *Loop) Approve() {
	l.planMode.Store(false)
}

// Interrupt cancels an in-flight Run.
func (l *Loop) Interrupt() {
	l.cancelMu.Lock()
	defer l.cancelMu.Unlock()
	if l.cancelRun != nil {
		l.cancelRun()
	}
}

// SetModel rebinds the Loop to a different model and provider. It guards only
// the model/provider fields with modelMu — NOT runMu — so the swap can happen
// concurrently with a running turn. Locking runMu here (which Run holds for the
// whole turn) would block every model switch and Provider() read until the
// in-flight turn finished, deadlocking the steering path. Run reads the provider
// under the same modelMu, so the swap stays tear-free. Safe from any goroutine.
func (l *Loop) SetModel(modelID string, p llm.Provider) {
	l.modelMu.Lock()
	defer l.modelMu.Unlock()
	l.cfg.Model = modelID
	l.cfg.Provider = p
	l.activeModel = modelID
}

// Provider returns the provider the Loop is currently bound to. It takes only a
// modelMu read lock (never runMu) so it is safe to call from any goroutine —
// including while a turn is running — and reflects the latest SetModel call.
func (l *Loop) Provider() llm.Provider {
	l.modelMu.RLock()
	defer l.modelMu.RUnlock()
	return l.cfg.Provider
}

// Steer queues text as a steering message for the in-flight Run. When a Run is
// active, the text is appended to the conversation as the next user message at
// the next safe boundary (after the current tool batch, before the next
// provider call), so the agent course-corrects without a full restart. It
// returns true when a Run was in flight and the message was queued onto it, and
// false when no Run was active; in the latter case the caller should start the
// text as a fresh turn. Steer is safe to call from any goroutine.
func (l *Loop) Steer(text string) (queued bool) {
	if text == "" {
		return false
	}
	l.steerMu.Lock()
	defer l.steerMu.Unlock()
	if !l.running {
		return false
	}
	l.steerQueue = append(l.steerQueue, text)
	return true
}

// drainSteering removes and returns all queued steering messages, converting
// each into a user message bound to sessionID. It returns nil when the queue is
// empty.
func (l *Loop) drainSteering(sessionID string) []message.Message {
	l.steerMu.Lock()
	defer l.steerMu.Unlock()
	if len(l.steerQueue) == 0 {
		return nil
	}
	out := make([]message.Message, 0, len(l.steerQueue))
	for _, text := range l.steerQueue {
		out = append(out, textMessage(sessionID, message.RoleUser, text))
	}
	l.steerQueue = l.steerQueue[:0]
	return out
}

// hasSteering reports whether any steering messages are queued.
func (l *Loop) hasSteering() bool {
	l.steerMu.Lock()
	defer l.steerMu.Unlock()
	return len(l.steerQueue) > 0
}

// PendingSteering drains and returns any steering messages that were queued but
// not consumed by a Run (for example, text that arrived between the loop's
// final steering check and the run mutex releasing). The TUI uses this after a
// turn finishes to start the leftover text as a fresh prompt so no steering
// message is lost. It returns the raw queued text in order.
func (l *Loop) PendingSteering() []string {
	l.steerMu.Lock()
	defer l.steerMu.Unlock()
	if len(l.steerQueue) == 0 {
		return nil
	}
	out := make([]string, len(l.steerQueue))
	copy(out, l.steerQueue)
	l.steerQueue = l.steerQueue[:0]
	return out
}

// Compact condenses the session's conversation in memory so the next provider
// request sends a smaller history. It loads the current on-disk history, runs
// it through the configured Compactor (or the default drop-and-mark Compactor),
// and stores the condensed result as an in-memory snapshot. Compaction never
// mutates the on-disk session: it only changes what Run sends to the provider
// on subsequent turns, exactly like truncateForContext.
//
// The system prompt is preserved automatically because it is carried in the
// Config and passed to the provider separately, never within the history. The
// most recent genuine user message is preserved explicitly: if the Compactor
// drops it, Compact re-appends it so the model always retains the live prompt.
func (l *Loop) Compact(ctx context.Context, sessionID string) error {
	history, err := l.cfg.Sessions.Messages(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("loading session messages: %w", err)
	}

	condensed, err := l.compactHistory(ctx, history)
	if err != nil {
		return err
	}

	l.compactMu.Lock()
	l.compacted = condensed
	l.compactedLen = len(history)
	l.compactMu.Unlock()

	return nil
}

// compactHistory runs history through the configured Compactor (or the default
// drop-and-mark Compactor) and enforces the preserve-latest-user invariant: if
// the Compactor dropped the most recent genuine user message, it is re-appended
// so the live prompt is never lost. The system prompt is preserved
// automatically because it is carried in the Config and sent to the provider
// separately, never within the history. The returned slice is a fresh copy.
func (l *Loop) compactHistory(ctx context.Context, history []message.Message) ([]message.Message, error) {
	compactor := l.cfg.Compactor
	if compactor == nil {
		compactor = l.defaultCompactor()
	}

	input := append([]message.Message(nil), history...)
	condensed, err := compactor.Compact(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("compacting history: %w", err)
	}
	condensed = append([]message.Message(nil), condensed...)

	// Preserve the most recent genuine user message: if the Compactor dropped
	// it, re-append it so the live prompt is never lost.
	if idx := latestUserIndex(history); idx >= 0 {
		latest := history[idx]
		if !containsMessage(condensed, latest) {
			condensed = append(condensed, latest)
		}
	}
	return condensed, nil
}

// defaultCompactor selects the Compactor used when the Config supplies none.
// When a provider is configured it returns the LLM-summary Compactor, which asks
// the model to write a structured checkpoint of the dropped prefix. With no
// provider it falls back to the drop-and-mark Compactor so compaction still
// works (for example, in tests that construct a Loop without a live provider
// path), preserving the prior behavior.
func (l *Loop) defaultCompactor() Compactor {
	if l.cfg.Provider != nil {
		return newLLMSummaryCompactor(l.cfg.Provider, l.activeModel, 2)
	}
	return newDropAndMarkCompactor(2)
}

// applyCompaction grafts messages that arrived after the last Compact call onto
// the condensed snapshot. When no compaction has occurred it returns history
// unchanged, so the normal turn path is unaffected.
func (l *Loop) applyCompaction(history []message.Message) []message.Message {
	l.compactMu.Lock()
	defer l.compactMu.Unlock()
	if l.compacted == nil {
		return history
	}
	out := append([]message.Message(nil), l.compacted...)
	if l.compactedLen <= len(history) {
		out = append(out, history[l.compactedLen:]...)
	}
	return out
}

// maybeAutoCompact triggers background compaction when the session has filled
// the model's context window beyond the configured threshold. It stores the
// compacted snapshot in memory so the next provider call sends a smaller
// history, and publishes EventAutoCompacted so the TUI can surface an inline
// notice. A zero or negative threshold disables auto-compaction entirely.
func (l *Loop) maybeAutoCompact(ctx context.Context, sessionID string, history []message.Message, inputTokens int) {
	threshold := l.cfg.AutoCompactThreshold
	if threshold <= 0 || threshold >= 1.0 {
		return
	}
	window := l.contextWindow()
	if window <= 0 || inputTokens <= 0 {
		return
	}
	fillPct := float64(inputTokens) / float64(window)
	if fillPct < threshold {
		return
	}
	condensed, err := l.compactHistory(ctx, history)
	if err != nil {
		slog.Warn("auto-compact failed",
			slog.String("session", sessionID),
			slog.Float64("fill_pct", fillPct),
			slog.String("error", err.Error()),
		)
		return
	}
	l.compactMu.Lock()
	l.compacted = condensed
	l.compactedLen = len(history)
	l.compactMu.Unlock()
	slog.Info("auto-compact triggered",
		slog.String("session", sessionID),
		slog.Float64("fill_pct", fillPct),
		slog.Float64("threshold", threshold),
	)
	l.publish(ctx, Event{SessionID: sessionID, AgentName: l.name, Kind: EventAutoCompacted})
}

// fitHistory ensures history fits the model's usable context window before it
// is sent to the provider. When the window is unknown (zero), it preserves the
// historical behavior of leaving history untouched. When history already fits,
// it is returned unchanged.
//
// On overflow it first invokes the Compactor to SUMMARIZE the conversation
// (preserving the system prompt automatically and the latest genuine user
// message explicitly) rather than hard-dropping. If the compacted history fits,
// it is used. If compaction does not free enough room, it falls back to
// drop-oldest truncation so the turn still proceeds.
//
// If the latest genuine user message alone exceeds the usable window, no
// strategy can make the turn fit; fitHistory returns ErrContextOverflow rather
// than looping or silently sending an over-window request.
func (l *Loop) fitHistory(ctx context.Context, history []message.Message) ([]message.Message, error) {
	window := l.contextWindow()
	if window <= 0 {
		// Unknown window: keep the prior behavior of sending history as-is.
		return history, nil
	}

	budget := fitBudget(window, l.framedSystemPrompt())
	if fitsBudget(history, budget) {
		return history, nil
	}

	// The latest genuine user message must always survive a fit. If it alone
	// exceeds the budget, no compaction or drop-oldest can rescue the turn.
	if idx := latestUserIndex(history); idx >= 0 {
		if estimateMessageTokens(history[idx]) > budget {
			return nil, ErrContextOverflow
		}
	}

	// Try compaction first: summarize rather than hard-drop.
	condensed, err := l.compactHistory(ctx, history)
	if err != nil {
		return nil, err
	}
	if fitsBudget(condensed, budget) {
		return condensed, nil
	}

	// Compaction did not free enough room: fall back to drop-oldest, which
	// always retains the latest user message.
	return truncateForContext(history, window), nil
}

// Run drives a single user turn.
func (l *Loop) Run(ctx context.Context, sessionID string, userMsg message.Message) error {
	if !l.runMu.TryLock() {
		panic("agent: Run called concurrently on one Loop")
	}

	runCtx, cancel := context.WithCancel(ctx)
	l.cancelMu.Lock()
	l.cancelRun = cancel
	l.cancelMu.Unlock()
	l.steerMu.Lock()
	l.running = true
	l.steerMu.Unlock()
	defer func() {
		cancel()
		l.cancelMu.Lock()
		l.cancelRun = nil
		l.cancelMu.Unlock()
		// Clear the final-turn flag so a subsequent Run on this Loop starts with
		// tools enabled and the base system prompt, regardless of how this Run
		// exited.
		l.finalTurn.Store(false)
		// Release the run mutex BEFORE clearing the running flag so a caller that
		// observes running==false (and starts a fresh Run) never finds the mutex
		// still held. Clearing running last also closes the window where Steer
		// could queue onto a turn that has already stopped draining.
		l.runMu.Unlock()
		l.steerMu.Lock()
		l.running = false
		l.steerMu.Unlock()
	}()

	// SessionEnd fires once when the run completes for any reason (normal
	// finish, step limit, loop detection, provider/persistence error, or
	// cancellation). It is deferred so every exit path is covered. The hook
	// fires with context.WithoutCancel so a cancelled run still runs its
	// SessionEnd command, since "cancels" is an explicit end-of-run case.
	defer l.fireHook(context.WithoutCancel(ctx), hooks.SessionEnd, hooks.SessionPayload{SessionID: sessionID})

	l.publish(runCtx, Event{SessionID: sessionID, AgentName: l.name, Kind: EventTurnStarted})
	userMsg.SessionID = sessionID
	if userMsg.Role == "" {
		userMsg.Role = message.RoleUser
	}
	if userMsg.CreatedAt.IsZero() {
		userMsg.CreatedAt = time.Now().UTC()
	}

	// UserPromptSubmit fires before the prompt is appended and the turn runs. A
	// matching hook may block the prompt — the model is never called and the turn
	// ends with a recorded notice — or return additional context that is appended
	// as a follow-on user message so the model sees it this turn without
	// polluting the goal frame (which keys off the first user message's own text).
	var injectedContext string
	if l.cfg.Hooks != nil {
		decision, err := l.cfg.Hooks.Fire(runCtx, hooks.UserPromptSubmit, hooks.PromptPayload{
			Prompt:    messageText(userMsg),
			SessionID: sessionID,
		})
		if err != nil {
			l.publish(runCtx, Event{SessionID: sessionID, AgentName: l.name, Kind: EventRunError, Err: err})
			return fmt.Errorf("firing UserPromptSubmit hook: %w", err)
		}
		if decision.Block {
			reason := strings.TrimSpace(decision.Reason)
			if reason == "" {
				reason = "no reason provided"
			}
			// Record the prompt and the block notice so the transcript reflects
			// what happened, then end the turn without calling the model.
			if err := l.cfg.Sessions.AppendMessage(runCtx, sessionID, userMsg); err != nil {
				return fmt.Errorf("appending user message: %w", err)
			}
			notice := textMessage(sessionID, message.RoleAssistant, "Prompt blocked by hook: "+reason)
			if err := l.cfg.Sessions.AppendMessage(runCtx, sessionID, notice); err != nil {
				return fmt.Errorf("appending block notice: %w", err)
			}
			l.publish(runCtx, Event{SessionID: sessionID, AgentName: l.name, Kind: EventTurnFinished, Message: &notice})
			return nil
		}
		injectedContext = strings.TrimSpace(decision.AdditionalContext)
	}

	if err := l.cfg.Sessions.AppendMessage(runCtx, sessionID, userMsg); err != nil {
		return fmt.Errorf("appending user message: %w", err)
	}
	// appended counts the messages this turn just wrote so the first-turn check
	// below stays correct whether or not a hook injected a follow-on context
	// message.
	appended := 1
	if injectedContext != "" {
		ctxMsg := textMessage(sessionID, message.RoleUser, injectedContext)
		if err := l.cfg.Sessions.AppendMessage(runCtx, sessionID, ctxMsg); err != nil {
			return fmt.Errorf("appending hook context: %w", err)
		}
		appended = 2
	}

	history, err := l.cfg.Sessions.Messages(runCtx, sessionID)
	if err != nil {
		return fmt.Errorf("loading session messages: %w", err)
	}

	// SessionStart fires when a session's first turn begins, not on every Run.
	// The messages this turn just appended are the only ones in history exactly
	// when this is the session's first turn, so a later Run on the same session
	// never refires it.
	if len(history) == appended {
		l.fireHook(runCtx, hooks.SessionStart, hooks.SessionPayload{SessionID: sessionID})
	}

	// Capture the user's original goal from the FULL on-disk history (before
	// compaction trims the in-memory copy) so systemPrompt can re-inject it as
	// an active frame every turn. The on-disk history always retains the first
	// user message, so this stays correct for the life of the session even once
	// compaction has dropped that message from the working history.
	l.setGoalFrame(firstUserGoal(history))

	// On the session's first turn, decide whether this is a small task — a
	// short file-generation request in an empty or near-empty workspace — and
	// if so render the concise system prompt for it. The choice is made once,
	// from the opening request and the directory state before any files are
	// written, and then held for the session: a follow-up turn must not flip to
	// the full coder prompt just because the first turn scaffolded files into
	// the (previously empty) directory. Complex repo edits leave the small
	// prompt empty and keep the full coder doctrine.
	if len(history) == appended {
		l.resolveSmallTask(history)
	}

	history = l.applyCompaction(history)

	// Resolve which model serves this turn BEFORE fitting history, so the whole
	// turn (every step, retry, usage record, and the context-window budget used
	// by fitHistory) uses one model. Routing only inspects the latest genuine
	// user message, which is present pre-fit and is guaranteed to survive a fit,
	// so deciding here yields the same choice as deciding post-fit. With no
	// Router this is exactly cfg.Model, preserving prior behavior.
	l.activeModel = l.resolveTurnModel(history)

	history, err = l.fitHistory(runCtx, history)
	if err != nil {
		l.publish(runCtx, Event{SessionID: sessionID, AgentName: l.name, Kind: EventRunError, Err: err})
		return err
	}

	detector := &loopDetector{}
	// Reset the per-Run policy-verification counter so each turn gets a fresh
	// budget of verify→fix cycles.
	l.verifyAttempts = 0

	for step := 0; step < l.cfg.MaxSteps; step++ {
		// On the final allowed step, switch to the tools-disabled handoff turn:
		// llmTools returns nothing and systemPrompt appends maxStepsPrompt, so the
		// model can only reply with a plain-text progress summary instead of
		// hitting the dead-end "step limit reached" line. The flag is cleared on
		// Run exit by the deferred reset above.
		finalStep := step == l.cfg.MaxSteps-1
		l.finalTurn.Store(finalStep)

		// Drain any steering messages queued via Steer at this safe boundary
		// (always after a complete tool batch, never between an assistant
		// tool_use and its tool_result). Each is appended as the next user
		// message so the agent course-corrects without restarting the turn.
		for _, steerMsg := range l.drainSteering(sessionID) {
			if err := l.cfg.Sessions.AppendMessage(runCtx, sessionID, steerMsg); err != nil {
				return fmt.Errorf("appending steering message: %w", err)
			}
			history = append(history, steerMsg)
		}

		assistant, pendingToolCalls, usage, err := l.callProviderWithRetry(runCtx, history)
		// Record the egress to the model — which provider/model the prompt was
		// sent to, and the outcome — before handling success or failure, so a
		// failed turn is audited just like a successful one.
		l.auditLLM(runCtx, sessionID, history, usage, err)
		if err != nil {
			failure := textMessage(sessionID, message.RoleAssistant, "provider failed: "+err.Error())
			_ = l.cfg.Sessions.AppendMessage(runCtx, sessionID, failure)
			l.publish(runCtx, Event{SessionID: sessionID, AgentName: l.name, Kind: EventRunError, Err: err})
			return fmt.Errorf("calling provider: %w", err)
		}
		if usage != nil {
			assistant.Usage = &message.TokenUsage{
				InputTokens:      usage.InputTokens,
				OutputTokens:     usage.OutputTokens,
				CacheReadTokens:  usage.CacheReadTokens,
				CacheWriteTokens: usage.CacheWriteTokens,
			}
			// Ledger recording is best-effort: a billing write failure (e.g.
			// unknown model in the pricing table, transient DB lock) must never
			// discard a completed, already-paid-for provider response. Log the
			// failure at Warn so it is visible for reconciliation and continue.
			if err := l.recordUsage(runCtx, sessionID, *usage); err != nil {
				slog.Warn("ledger record failed — usage not billed locally",
					slog.String("session", sessionID),
					slog.String("model", l.activeModel),
					slog.Int("input_tokens", usage.InputTokens),
					slog.Int("output_tokens", usage.OutputTokens),
					slog.String("error", err.Error()),
				)
			}
		}
		if err := l.cfg.Sessions.AppendMessage(runCtx, sessionID, assistant); err != nil {
			return fmt.Errorf("appending assistant message: %w", err)
		}
		l.publish(runCtx, Event{SessionID: sessionID, AgentName: l.name, Kind: EventLLMResponse, Message: &assistant})
		history = append(history, assistant)

		// Auto-compact when the context fill percentage meets the configured
		// threshold. This primes the in-memory compaction snapshot so the NEXT
		// provider call sends a smaller history, preventing the turn from
		// silently overflowing the context window. The compact is best-effort:
		// a failure is logged and does not abort the current turn.
		if usage != nil {
			l.maybeAutoCompact(runCtx, sessionID, history, usage.InputTokens)
		}

		if finalStep {
			// The handoff turn ran with no tools, so the model's plain-text summary
			// is the recorded final reply. Any tool calls it may still have emitted
			// are intentionally ignored: this turn ends the run gracefully rather
			// than executing more work past the step limit.
			l.publish(runCtx, Event{SessionID: sessionID, AgentName: l.name, Kind: EventTurnFinished, Message: &assistant})
			return nil
		}

		if len(pendingToolCalls) == 0 {
			// The model would end the turn, but if steering arrived while it was
			// composing this reply, keep going so the queued message is consumed
			// as the next user message instead of being deferred to a restart.
			if l.hasSteering() {
				continue
			}
			l.publish(runCtx, Event{SessionID: sessionID, AgentName: l.name, Kind: EventTurnFinished})
			return nil
		}

		// Fan out all calls in the batch concurrently when every call is a
		// read-only tool (view, grep, glob, navigate, symbols, …). Sequential
		// execution is used for any batch that contains a write-class tool or
		// an unknown tool name. A nil batchResults signals "use sequential".
		batchResults := l.maybeFanOutReadOnly(runCtx, sessionID, pendingToolCalls)

		var stopTurnAfterBatch bool
		for i, call := range pendingToolCalls {
			callHash, err := toolCallHash(call.Name, call.Input)
			if err != nil {
				return fmt.Errorf("checking tool loop: %w", err)
			}
			// Predict an identical-result run before paying for it: when the prior
			// observations are already identical and this call matches them, running
			// it would only reproduce the same futile result, so trip now rather than
			// execute the K-th identical call.
			if detector.wouldRepeat(callHash) {
				// Synthesize placeholder results for this call and all remaining
				// unexecuted calls so the conversation history stays well-formed
				// (every tool_use must have a matching tool_result before the next
				// provider request, or the provider rejects with a 400).
				return l.tripLoopGuard(runCtx, sessionID, pendingToolCalls[i:])
			}

			var result tools.Result
			if batchResults != nil {
				// Parallel path: results were already computed concurrently;
				// read-only tools never fire file-edit hooks or verify commands.
				result = batchResults[i]
			} else {
				result = l.runTool(runCtx, sessionID, call)
				l.fireFileEditHook(runCtx, sessionID, call, result)

				// Run any verify_command configured on matching FileEdit hooks. When
				// verification fails, override the result with the synthesized error
				// so the model sees the failure. The original tool result is
				// discarded: the verify output is more actionable than the success
				// message from the write itself. When verification is not configured
				// (the common case), verifyResult is nil and result is unchanged.
				if verifyResult := l.runVerifyCommands(runCtx, call, result); verifyResult != nil {
					result = *verifyResult
				}

				// Policy-driven verification: when no user-declared per-file verifier
				// governs this edit and the change class requires verification,
				// discover the project's own check (go test ./..., npm run build, …),
				// run it, and fold success or failure into the result so simple app
				// generation verifies automatically and a failure is fed back to the
				// model. runPolicyVerify defers to per-file verifiers and is bounded
				// by MaxVerifyAttempts so the verify→fix cycle cannot loop forever.
				if verifyResult := l.runPolicyVerify(runCtx, call, result); verifyResult != nil {
					result = *verifyResult
				}
			}

			// Record the executed (call,result) pair. A true return means the recent
			// history oscillates A,B,A,B — a distinct futile pattern the predictive
			// check cannot see because the calls differ each step.
			if detector.record(callHash, resultHash(result.Content), result.IsError) {
				// The current call ran and produced a result, but we are aborting
				// the batch. Append this call's real result, then synthesize
				// placeholders for all remaining unexecuted calls.
				content := truncateToolResult(result.Content, l.toolResultMaxBytes(), result.IsError)
				toolMsg := message.Message{
					SessionID: sessionID,
					Role:      message.RoleUser,
					Content: []message.ContentBlock{message.ToolResultBlock{
						ToolUseID: call.ID,
						Content:   content,
						IsError:   result.IsError,
					}},
					CreatedAt: time.Now().UTC(),
				}
				if err := l.cfg.Sessions.AppendMessage(runCtx, sessionID, toolMsg); err != nil {
					return fmt.Errorf("appending tool result: %w", err)
				}
				history = append(history, toolMsg)
				// Synthesize placeholders for calls that did not run.
				return l.tripLoopGuard(runCtx, sessionID, pendingToolCalls[i+1:])
			}

			// Bound oversized tool output before it enters the conversation
			// history so a single giant result cannot blow the context window.
			// Error results pass through so their essential message stays intact.
			content := truncateToolResult(result.Content, l.toolResultMaxBytes(), result.IsError)
			toolMsg := message.Message{
				SessionID: sessionID,
				Role:      message.RoleUser,
				Content: []message.ContentBlock{message.ToolResultBlock{
					ToolUseID: call.ID,
					Content:   content,
					IsError:   result.IsError,
				}},
				CreatedAt: time.Now().UTC(),
			}
			if err := l.cfg.Sessions.AppendMessage(runCtx, sessionID, toolMsg); err != nil {
				return fmt.Errorf("appending tool result: %w", err)
			}
			history = append(history, toolMsg)

			// Forward any image the tool attached (e.g. view of an image file)
			// to the model as a real image block, so vision models see the
			// pixels rather than only the text placeholder in the tool result.
			if err := l.maybeAppendToolImage(runCtx, sessionID, call, result, &history); err != nil {
				return err
			}

			// When a tool signals that its result ends the turn, record the
			// remaining unexecuted calls as aborted so history stays well-formed,
			// then finish the turn cleanly after the batch.
			if result.StopTurn {
				if err := l.appendOrphanResults(runCtx, sessionID, pendingToolCalls[i+1:], "tool not executed: preceding tool requested turn stop"); err != nil {
					return err
				}
				stopTurnAfterBatch = true
				break
			}
		}
		if stopTurnAfterBatch {
			l.publish(runCtx, Event{SessionID: sessionID, AgentName: l.name, Kind: EventTurnFinished})
			return nil
		}
	}

	// Unreachable: MaxSteps is always >= 1 (New defaults a non-positive value),
	// so the loop runs at least once and the final-step branch returns from
	// inside the loop after the tools-disabled handoff turn. This return only
	// satisfies the compiler.
	return nil
}

// tripLoopGuard records the loop-detected outcome: it folds ErrLoopDetected
// into the session as an assistant message and publishes EventLoopDetected,
// then returns nil so Run ends gracefully without surfacing an error. Both
// detector signals (predictive identical-run and A,B,A,B cycle) funnel through
// here so the trip is reported identically regardless of which pattern fired.
//
// unexecuted holds the tool calls from the current batch that did not run
// before the guard tripped. A synthetic error tool_result is appended for
// each one so the conversation history never contains an assistant tool_use
// block without a matching tool_result, which providers reject with a 400.
func (l *Loop) tripLoopGuard(ctx context.Context, sessionID string, unexecuted []pendingToolCall) error {
	if err := l.appendOrphanResults(ctx, sessionID, unexecuted, "tool not executed: loop detected"); err != nil {
		return err
	}
	msg := textMessage(sessionID, message.RoleAssistant, ErrLoopDetected.Error())
	if err := l.cfg.Sessions.AppendMessage(ctx, sessionID, msg); err != nil {
		return fmt.Errorf("appending loop-detection message: %w", err)
	}
	l.publish(ctx, Event{SessionID: sessionID, AgentName: l.name, Kind: EventLoopDetected, Message: &msg})
	return nil
}

// appendOrphanResults synthesizes a placeholder error tool_result for each
// call in orphans and persists it to the session. This keeps the conversation
// history well-formed after any early batch exit (loop detection, StopTurn,
// or interruption): providers require every tool_use block to have a matching
// tool_result before the next turn's request, and reject a 400 otherwise.
// The reason string is included in each synthetic result's content so it is
// visible in the recorded session for debugging.
func (l *Loop) appendOrphanResults(ctx context.Context, sessionID string, orphans []pendingToolCall, reason string) error {
	for _, call := range orphans {
		toolMsg := message.Message{
			SessionID: sessionID,
			Role:      message.RoleUser,
			Content: []message.ContentBlock{message.ToolResultBlock{
				ToolUseID: call.ID,
				Content:   reason,
				IsError:   true,
			}},
			CreatedAt: time.Now().UTC(),
		}
		if err := l.cfg.Sessions.AppendMessage(ctx, sessionID, toolMsg); err != nil {
			return fmt.Errorf("appending orphan tool result for %s: %w", call.Name, err)
		}
	}
	return nil
}

type pendingToolCall struct {
	ID    string
	Name  string
	Input json.RawMessage
}

func (l *Loop) callProvider(ctx context.Context, history []message.Message) (message.Message, []pendingToolCall, *llm.Usage, error) {
	req := llm.Request{
		Model:        l.activeModel,
		Messages:     history,
		Tools:        l.llmTools(),
		SystemPrompt: l.systemPrompt(),
	}
	l.applyReasoning(&req)
	events, err := l.cfg.Provider.Stream(ctx, req)
	if err != nil {
		return message.Message{}, nil, nil, err
	}

	var text string
	var calls []pendingToolCall
	var usage *llm.Usage
	openCalls := make(map[string]*pendingToolCall)
	for {
		select {
		case <-ctx.Done():
			return message.Message{}, nil, nil, fmt.Errorf("reading provider stream: %w", ctx.Err())
		case ev, ok := <-events:
			if !ok {
				blocks := []message.ContentBlock{}
				if text != "" || len(calls) == 0 {
					blocks = append(blocks, message.TextBlock{Text: text})
				}
				for _, call := range calls {
					if len(call.Input) == 0 {
						call.Input = json.RawMessage(`{}`)
					}
					blocks = append(blocks, message.ToolUseBlock{ID: call.ID, Name: call.Name, Input: call.Input})
				}
				return message.Message{
					Role:      message.RoleAssistant,
					Content:   blocks,
					CreatedAt: time.Now().UTC(),
				}, calls, usage, nil
			}
			switch e := ev.(type) {
			case llm.DeltaTextEvent:
				text += e.Text
			case llm.ToolUseStartEvent:
				call := &pendingToolCall{ID: e.ID, Name: e.Name}
				openCalls[e.ID] = call
				calls = append(calls, *call)
			case llm.ToolUseDeltaEvent:
				call, ok := openCalls[e.ID]
				if !ok {
					call = &pendingToolCall{ID: e.ID}
					openCalls[e.ID] = call
					calls = append(calls, *call)
				}
				call.Input = append(call.Input, []byte(e.Delta)...)
				for i := range calls {
					if calls[i].ID == e.ID {
						calls[i] = *call
					}
				}
			case llm.ToolUseEndEvent:
				call, ok := openCalls[e.ID]
				if !ok {
					calls = append(calls, pendingToolCall{ID: e.ID, Name: e.Name, Input: e.Input})
					continue
				}
				call.Name = e.Name
				call.Input = e.Input
				for i := range calls {
					if calls[i].ID == e.ID {
						calls[i] = *call
					}
				}
			case llm.EndEvent:
				u := e.Usage
				usage = &u
			case llm.ErrorEvent:
				return message.Message{}, nil, nil, e.Err
			}
		}
	}
}

// systemPrompt returns the system prompt for the current provider call. On the
// tools-disabled final turn granted at the step limit it appends maxStepsPrompt
// so the model produces a progress handoff. In plan mode it appends the
// plan-mode instruction so the agent is prompted to produce a plan rather than
// execute; once Approve clears plan mode, the base prompt is used unchanged.
func (l *Loop) systemPrompt() string {
	base := l.framedSystemPrompt()
	if l.finalTurn.Load() {
		return base + maxStepsPrompt
	}
	if l.planMode.Load() {
		return base + planModePrompt
	}
	return base
}

// framedSystemPrompt returns the base system prompt with the active-goal frame
// appended when one has been captured. It is the stable prefix shared by every
// provider call this turn (the plan-mode and max-steps reminders are layered on
// top in systemPrompt), so fitHistory budgets against it to reserve room for the
// re-injected goal.
func (l *Loop) framedSystemPrompt() string {
	base := l.cfg.SystemPrompt
	if small := l.smallTask(); small != "" {
		// Small-task mode swaps the full coder doctrine for the concise prompt,
		// trimming the input tokens a simple from-scratch generation does not need.
		base = small
	}
	if goal := l.goalFrame(); goal != "" {
		base += goalFrameHeader + goal + goalFrameFooter
	}
	return base
}

// smallTask returns the concise small-task system prompt when the current Run
// is operating in small-task mode, or "" otherwise.
func (l *Loop) smallTask() string {
	l.smallMu.Lock()
	defer l.smallMu.Unlock()
	return l.smallTaskPrompt
}

// resolveSmallTask decides whether the current turn is a small task and, when
// it is, renders and stores the concise system prompt framedSystemPrompt uses
// in place of the full coder doctrine. A small task is a short, from-scratch
// file-generation request (isSmallTaskPrompt) issued in an empty or near-empty
// workspace (directoryIsEmptyish); both conditions must hold. The decision is
// declined for the read-only task agent, which already runs its own lean
// exploration prompt, and for any turn whose configured system prompt is empty
// (nothing to trim). When the conditions do not hold the small prompt is left
// empty, so the turn keeps the full coder prompt unchanged.
func (l *Loop) resolveSmallTask(history []message.Message) {
	l.smallMu.Lock()
	defer l.smallMu.Unlock()
	l.smallTaskPrompt = ""
	if l.name == "task" || l.cfg.SystemPrompt == "" {
		return
	}
	if !isSmallTaskPrompt(firstUserGoal(history)) {
		return
	}
	if !directoryIsEmptyish(l.workDir()) {
		return
	}
	l.smallTaskPrompt = buildSmallTaskPrompt(l.cfg.Tools.List())
}

// goalFrame returns the captured active-goal text, or "" when none has been set.
func (l *Loop) goalFrame() string {
	l.goalMu.Lock()
	defer l.goalMu.Unlock()
	return l.goalFrameText
}

// setGoalFrame records the active-goal text re-injected by systemPrompt.
func (l *Loop) setGoalFrame(text string) {
	l.goalMu.Lock()
	defer l.goalMu.Unlock()
	l.goalFrameText = text
}

// firstUserGoal returns the trimmed text of the first user message in history,
// truncated to maxGoalFrameRunes, or "" when no user message carries text. It is
// the user's original objective that the Loop re-injects as the active-goal
// frame each turn.
func firstUserGoal(history []message.Message) string {
	for _, m := range history {
		if m.Role != message.RoleUser {
			continue
		}
		text := strings.TrimSpace(messageText(m))
		if text == "" {
			continue
		}
		runes := []rune(text)
		if len(runes) > maxGoalFrameRunes {
			return string(runes[:maxGoalFrameRunes]) + "…"
		}
		return text
	}
	return ""
}

// messageText concatenates the text blocks of m in order, separating them with
// newlines. Non-text blocks (tool calls, attachments) are ignored.
func messageText(m message.Message) string {
	var b strings.Builder
	for _, block := range m.Content {
		var text string
		switch t := block.(type) {
		case message.TextBlock:
			text = t.Text
		case *message.TextBlock:
			text = t.Text
		default:
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(text)
	}
	return b.String()
}

func (l *Loop) runTool(ctx context.Context, sessionID string, call pendingToolCall) (result tools.Result) {
	// Record the invocation on every return path — unknown tool, error result,
	// recovered panic, or success — so the audit log proves exactly what ran.
	defer func() { l.auditTool(ctx, sessionID, call.Name, result) }()
	tool, ok := l.cfg.Tools.Get(call.Name)
	if !ok {
		return tools.Result{Content: "unknown tool: " + call.Name, IsError: true}
	}
	wrapped := hookedTool{inner: tool, hooks: l.cfg.Hooks, sessionID: sessionID, agentName: l.name, allowed: l.toolAllowed(call.Name)}
	l.publish(ctx, Event{SessionID: sessionID, AgentName: l.name, Kind: EventToolCalled, ToolName: call.Name, ToolInput: call.Input})
	result, err := l.runToolSafely(ctx, &wrapped, call)
	if err != nil {
		l.publish(ctx, Event{SessionID: sessionID, AgentName: l.name, Kind: EventRunError, ToolName: call.Name, Err: err})
		return tools.Result{Content: err.Error(), IsError: true}
	}
	// Carry the same truncated content that the caller appends to history so
	// the live render and the persisted turn show identical output.
	content := truncateToolResult(result.Content, l.toolResultMaxBytes(), result.IsError)
	l.publish(ctx, Event{SessionID: sessionID, AgentName: l.name, Kind: EventToolResult, ToolName: call.Name, ToolResult: content})
	return result
}

// maybeFanOutReadOnly runs all calls in the batch concurrently when every call
// resolves to a tools.ReadOnlyTool. It returns nil when any call is not
// read-only (or the tool name is unknown) — the caller must fall through to
// the sequential path. Single-call batches always return nil since concurrency
// adds no benefit there. The returned slice is in the same order as calls.
func (l *Loop) maybeFanOutReadOnly(ctx context.Context, sessionID string, calls []pendingToolCall) []tools.Result {
	if len(calls) <= 1 {
		return nil
	}
	for _, call := range calls {
		t, ok := l.cfg.Tools.Get(call.Name)
		if !ok || !tools.IsReadOnly(t) {
			return nil
		}
	}
	results := make([]tools.Result, len(calls))
	var wg sync.WaitGroup
	wg.Add(len(calls))
	for i, call := range calls {
		go func(idx int, c pendingToolCall) {
			defer wg.Done()
			results[idx] = l.runTool(ctx, sessionID, c)
		}(i, call)
	}
	wg.Wait()
	return results
}

// runToolSafely runs the tool and converts any panic into an error so a single
// misbehaving tool cannot take down the agent loop. The recovered panic is
// logged with its stack and surfaced to the caller as an error.
func (l *Loop) runToolSafely(ctx context.Context, wrapped *hookedTool, call pendingToolCall) (result tools.Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			slog.Error(
				"tool panicked",
				slog.String("tool", call.Name),
				slog.Any("panic", r),
				slog.String("stack", string(stack)),
			)
			err = fmt.Errorf("tool %q panicked: %v", call.Name, r)
			result = tools.Result{}
		}
	}()
	return wrapped.Run(ctx, call.Input)
}

func (l *Loop) llmTools() []llm.Tool {
	// The final, tools-disabled handoff turn sends no tools so the model can only
	// reply with the plain-text progress summary maxStepsPrompt asks for.
	if l.finalTurn.Load() {
		return []llm.Tool{}
	}
	out := []llm.Tool{}
	for _, tool := range l.cfg.Tools.List() {
		if !l.toolAllowed(tool.Name()) {
			continue
		}
		out = append(out, llm.Tool{
			Name:        tool.Name(),
			Description: tool.Description(),
			InputSchema: tool.Schema(),
		})
	}
	return out
}

// toolAllowed reports whether the named tool may run on this turn. It applies
// the configured allow-list first (when non-empty, only listed tools pass), then
// layers plan mode on top, which can only further restrict the set to read-only
// tools. When plan mode is off and no allow-list is configured this reduces to
// "always allowed", preserving the unrestricted default. The two effects
// intersect — plan mode never expands a configured allow-list — so a custom
// agent restricted to a few tools does not gain new ones when plan mode turns
// on.
func (l *Loop) toolAllowed(name string) bool {
	if len(l.allowed) > 0 {
		if _, ok := l.allowed[name]; !ok {
			return false
		}
	}
	if l.planMode.Load() {
		if _, ok := readOnlySet[name]; !ok {
			return false
		}
	}
	return true
}

func (l *Loop) contextWindow() int {
	for _, model := range l.cfg.Provider.Models() {
		if model.ID == l.activeModel {
			return model.ContextWindow
		}
	}
	return 0
}

// applyReasoning populates the per-model reasoning controls on req from the
// active model's configuration. ReasoningEffort is forwarded only when
// configured non-empty, and Thinking only when a positive budget is configured.
// Both are gated by the provider against the model id, so populating them for a
// model that does not support the feature is harmless: the provider omits the
// unsupported field rather than failing the request. Models that configure
// neither leave req unchanged, preserving prior behavior.
func (l *Loop) applyReasoning(req *llm.Request) {
	model, ok := findModelByID(l.cfg.Provider.Models(), l.activeModel)
	if !ok {
		return
	}
	if model.ReasoningEffort != "" {
		req.ReasoningEffort = model.ReasoningEffort
	}
	if model.ThinkingBudget > 0 {
		req.Thinking = &llm.ThinkingConfig{BudgetTokens: model.ThinkingBudget}
	}
}

// maybeAppendToolImage forwards an image a tool attached to its Result back to
// the model as a real ImageBlock. The view tool reads image files into
// Result.Metadata (base64 bytes + MIME type) while its tool_result block carries
// only a text placeholder, so without this a vision model would never see the
// pixels. The image is delivered as a follow-up user message — mirroring how
// tool results are themselves separate user messages — and only when the active
// model advertises image support, since otherwise the provider would reject the
// turn. Malformed or absent metadata is skipped silently: the text placeholder
// already informs the model, so a bad image must never break the turn.
func (l *Loop) maybeAppendToolImage(ctx context.Context, sessionID string, call pendingToolCall, result tools.Result, history *[]message.Message) error {
	if result.IsError || len(result.Metadata) == 0 {
		return nil
	}
	encoded, ok := result.Metadata[tools.MetadataImage].(string)
	if !ok || encoded == "" {
		return nil
	}
	if !l.activeModelSupportsImages() {
		return nil
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(data) == 0 {
		return nil
	}
	mimeType, _ := result.Metadata[tools.MetadataMimeType].(string)
	if mimeType == "" {
		mimeType = "image/png"
	}
	imgMsg := message.Message{
		SessionID: sessionID,
		Role:      message.RoleUser,
		Content: []message.ContentBlock{
			message.TextBlock{Text: fmt.Sprintf("Image returned by the %s tool:", call.Name)},
			message.ImageBlock{MimeType: mimeType, Data: data},
		},
		CreatedAt: time.Now().UTC(),
	}
	if err := l.cfg.Sessions.AppendMessage(ctx, sessionID, imgMsg); err != nil {
		return fmt.Errorf("appending tool image: %w", err)
	}
	*history = append(*history, imgMsg)
	return nil
}

// activeModelSupportsImages reports whether the model selected for the current
// turn advertises image input. It returns false when the model is unknown so
// images are withheld from providers that would reject them.
func (l *Loop) activeModelSupportsImages() bool {
	model, ok := findModelByID(l.cfg.Provider.Models(), l.activeModel)
	return ok && model.SupportsImages
}

// findModelByID returns the model with the given id from models and whether it
// was found.
func findModelByID(models []llm.Model, id string) (llm.Model, bool) {
	for _, m := range models {
		if m.ID == id {
			return m, true
		}
	}
	return llm.Model{}, false
}

// resolveTurnModel picks the model for this turn. With no Router it returns the
// configured default unchanged, so routing is strictly opt-in. With a Router it
// asks the policy to choose from the provider's models; an empty or unknown
// choice falls back to the configured default so a declining Router never
// breaks the turn.
func (l *Loop) resolveTurnModel(history []message.Message) string {
	if l.cfg.Router == nil {
		return l.cfg.Model
	}
	models := l.cfg.Provider.Models()
	turn := Turn{
		History:        history,
		ToolsAvailable: len(l.llmTools()) > 0,
		Hint:           l.cfg.RouteHint,
	}
	choice := l.cfg.Router.Route(turn, models)
	if choice == "" {
		return l.cfg.Model
	}
	for _, m := range models {
		if m.ID == choice {
			return choice
		}
	}
	return l.cfg.Model
}

func (l *Loop) recordUsage(ctx context.Context, sessionID string, usage llm.Usage) error {
	if l.cfg.Ledger == nil {
		return nil
	}
	return l.cfg.Ledger.Record(ctx, ledger.Entry{
		ID:           newID(),
		SessionID:    sessionID,
		Provider:     l.cfg.Provider.Name(),
		Model:        l.activeModel,
		InputTokens:  usage.InputTokens,
		OutputTokens: usage.OutputTokens,
		At:           time.Now(),
	})
}

func (l *Loop) publish(ctx context.Context, ev Event) {
	if l.cfg.Bus != nil {
		l.cfg.Bus.Publish(ctx, ev)
	}
}

// fireHook fires a lifecycle hook, guarding on a nil hooks engine and logging
// (rather than propagating) any error so a misconfigured hook never aborts the
// agent run. Lifecycle hooks are best-effort observers: their failure must not
// fail the turn the way a blocking PreToolUse decision does.
func (l *Loop) fireHook(ctx context.Context, event hooks.Event, payload any) {
	if l.cfg.Hooks == nil {
		return
	}
	if _, err := l.cfg.Hooks.Fire(ctx, event, payload); err != nil {
		slog.Warn("Lifecycle hook failed", "event", event, "error", err)
	}
}

// fireFileEditHook fires a FileEdit hook after a successful write-class tool
// run. It returns immediately for non-mutating tools, error results (which
// covers hook-blocked and panicking tools), and inputs without a path.
func (l *Loop) fireFileEditHook(ctx context.Context, sessionID string, call pendingToolCall, result tools.Result) {
	if _, ok := writeClassTools[call.Name]; !ok {
		return
	}
	if result.IsError {
		return
	}
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(call.Input, &args); err != nil || args.Path == "" {
		return
	}
	l.fireHook(ctx, hooks.FileEdit, hooks.FileEditPayload{Path: args.Path, SessionID: sessionID})
}

// runVerifyCommands runs any verify_command entries from FileEdit hooks that
// match the edited file path. It is called after a write-class tool succeeds.
// On a non-write tool, a failed write, or when no verify hooks are configured,
// it is a no-op and returns nil. When a verify command exits non-zero it returns
// a synthesized error result (IsError=true) containing the command output so
// the model sees the failure and can re-edit or explain; on success it returns
// nil. The method uses the Loop's configured VerifyRunner, falling back to
// execVerifyRunner when none is set.
func (l *Loop) runVerifyCommands(ctx context.Context, call pendingToolCall, result tools.Result) *tools.Result {
	if _, ok := writeClassTools[call.Name]; !ok {
		return nil
	}
	if result.IsError {
		return nil
	}
	if l.cfg.VerifyHooks == nil {
		return nil
	}
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(call.Input, &args); err != nil || args.Path == "" {
		return nil
	}

	specs := l.cfg.VerifyHooks.MatchingVerifiers(args.Path)
	if len(specs) == 0 {
		return nil
	}

	runner := l.cfg.VerifyRunner
	if runner == nil {
		runner = execVerifyRunner{}
	}

	for _, spec := range specs {
		output, err := runner.RunVerify(ctx, spec.Command, spec.Cwd, spec.Timeout)
		if err != nil {
			slog.Info("Verify command failed",
				slog.String("command", spec.Command),
				slog.String("file", args.Path),
				slog.String("error", err.Error()),
			)
			content := fmt.Sprintf("verify_command failed for %s:\n$ %s\n%s\nerror: %s",
				args.Path, spec.Command, output, err.Error())
			r := tools.Result{Content: content, IsError: true}
			return &r
		}
		slog.Debug("Verify command passed",
			slog.String("command", spec.Command),
			slog.String("file", args.Path),
		)
	}
	return nil
}

// runPolicyVerify is the policy-driven counterpart to runVerifyCommands: it
// runs after a successful write-class tool that no user-declared per-file
// verifier handled, and applies BharatCode's verification policy (the T8
// Verification config) to decide whether the change must be verified. When it
// must, it discovers the project's own check (T9 DiscoverVerifyCommands —
// `go test ./...`, `npm run build`, …), runs it, and folds the outcome into the
// tool result so the verification command and its result reach the model and
// the transcript.
//
// On a verify failure it returns an IsError result carrying the command and its
// output so the model sees the failure and re-edits. On success it returns a
// non-error result whose content appends a "Verified" line naming the command,
// so the final answer can report exactly what was run. It returns nil — leaving
// the original result untouched — whenever verification does not apply: a
// non-write or failed tool, a tool that did not signal a verify-needing change,
// a disabled policy or a change class the policy does not gate, no discoverable
// command for the workspace, or an exhausted attempt budget. The budget
// (MaxVerifyAttempts) bounds the verify→fix→verify cycle so a model that cannot
// fix the failure does not loop forever.
func (l *Loop) runPolicyVerify(ctx context.Context, call pendingToolCall, result tools.Result) *tools.Result {
	if !l.writeProducedChange(call, result) {
		return nil
	}

	path := callPath(call)

	// A user-declared per-file verify_command already governs this edit
	// (runVerifyCommands ran it, pass or fail). Defer to it rather than running a
	// second, discovered command, so verification happens exactly once.
	if l.cfg.VerifyHooks != nil && len(l.cfg.VerifyHooks.MatchingVerifiers(path)) > 0 {
		return nil
	}

	trigger := verifyTriggerForPath(path)
	if !l.cfg.Verification.RequiresVerification(trigger) {
		return nil
	}

	// Stop re-verifying once the budget is spent so the verify→fix cycle cannot
	// loop forever; the model still gets to explain the unresolved failure on the
	// next turn since the original (success) result is left in place.
	if l.verifyAttempts >= l.maxVerifyAttempts() {
		slog.Info("Policy verification budget exhausted; skipping further verification",
			slog.Int("attempts", l.verifyAttempts),
		)
		return nil
	}

	command := l.discoverVerifyCommand()
	if command == "" {
		// Nothing recognizable to run: this is the sanctioned no_test_command
		// skip. Leave the edit to the model rather than fabricating a check.
		return nil
	}

	runner := l.cfg.VerifyRunner
	if runner == nil {
		runner = execVerifyRunner{}
	}
	l.verifyAttempts++

	output, err := runner.RunVerify(ctx, command, l.workDir(), 0)
	if err != nil {
		slog.Info("Policy verification failed",
			slog.String("command", command),
			slog.Int("attempt", l.verifyAttempts),
			slog.String("error", err.Error()),
		)
		content := fmt.Sprintf("verification failed:\n$ %s\n%s\nerror: %s\nFix the change and re-run; the edit is not done until verification passes.",
			command, output, err.Error())
		return &tools.Result{Content: content, IsError: true}
	}

	slog.Debug("Policy verification passed", slog.String("command", command))
	// Surface the command in the (successful) result so the model can report it
	// verbatim in the final answer. The original success content is preserved.
	content := strings.TrimRight(result.Content, "\n")
	if content != "" {
		content += "\n\n"
	}
	content += "Verified: $ " + command + " (passed)"
	verified := result
	verified.Content = content
	return &verified
}

// writeProducedChange reports whether call is a successful write-class tool run
// that should be considered for verification. It honors the tool's explicit
// VerifyNeeded signal when set, and falls back to the writeClassTools name map
// so verification still triggers for tools that have not yet adopted the field.
func (l *Loop) writeProducedChange(call pendingToolCall, result tools.Result) bool {
	if result.IsError {
		return false
	}
	if result.VerifyNeeded {
		return true
	}
	_, ok := writeClassTools[call.Name]
	return ok
}

// discoverVerifyCommand returns the highest-confidence verify command for the
// workspace, or "" when nothing recognizable was found. DiscoverVerifyCommands
// returns candidates sorted by descending confidence, so the first is the best.
func (l *Loop) discoverVerifyCommand() string {
	candidates := DiscoverVerifyCommands(l.workDir())
	if len(candidates) == 0 {
		return ""
	}
	return candidates[0].Command
}

// workDir returns the repo root used for verify discovery and command
// execution, defaulting to the current directory when unconfigured.
func (l *Loop) workDir() string {
	if l.cfg.WorkDir == "" {
		return "."
	}
	return l.cfg.WorkDir
}

// maxVerifyAttempts returns the configured cap on policy-driven verify→fix
// cycles, defaulting to defaultMaxVerifyAttempts for a non-positive value.
func (l *Loop) maxVerifyAttempts() int {
	if l.cfg.MaxVerifyAttempts > 0 {
		return l.cfg.MaxVerifyAttempts
	}
	return defaultMaxVerifyAttempts
}

// callPath extracts the "path" argument from a tool call's JSON input, or ""
// when the input has no path. It mirrors the inline decode used by the file-edit
// hooks so the verification path classifier sees the same path they do.
func callPath(call pendingToolCall) string {
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(call.Input, &args); err != nil {
		return ""
	}
	return args.Path
}

// verifyTriggerForPath classifies an edited file path into the verification
// trigger that governs it. The mapping mirrors the policy vocabulary: package
// manifests and test/build files have their own classes, and everything else a
// write-class tool produces is treated as a source edit. An empty path falls
// back to source_edit so an unattributed change is still gated by the default
// policy rather than silently escaping it.
func verifyTriggerForPath(path string) config.VerificationTrigger {
	base := strings.ToLower(filepath.Base(path))
	switch base {
	case "go.mod", "go.sum", "package.json", "package-lock.json", "pnpm-lock.yaml",
		"yarn.lock", "cargo.toml", "cargo.lock", "pyproject.toml", "setup.py",
		"setup.cfg", "requirements.txt", "gemfile", "go.work":
		return config.VerifyTriggerPackageManifest
	case "makefile", "dockerfile":
		return config.VerifyTriggerTestOrBuildFile
	}
	if strings.HasSuffix(base, "_test.go") ||
		strings.HasSuffix(base, ".test.ts") || strings.HasSuffix(base, ".test.js") ||
		strings.HasSuffix(base, ".spec.ts") || strings.HasSuffix(base, ".spec.js") {
		return config.VerifyTriggerTestOrBuildFile
	}
	return config.VerifyTriggerSourceEdit
}

func textMessage(sessionID string, role message.Role, text string) message.Message {
	return message.Message{
		SessionID: sessionID,
		Role:      role,
		Content:   []message.ContentBlock{message.TextBlock{Text: text}},
		CreatedAt: time.Now().UTC(),
	}
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("agent-%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
