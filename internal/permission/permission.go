// Package permission provides tool execution gating, user approval prompt loops,
// allow-list scanning, yolo bypasses, and decision persistence.
package permission

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
)

// ApprovalMode controls the global gating policy applied before the interactive prompt fallback.
type ApprovalMode string

const (
	// ApprovalReadOnly auto-denies any tool that writes or executes; only read-class tools are auto-allowed.
	ApprovalReadOnly ApprovalMode = "ReadOnly"
	// ApprovalAuto is the default behavior: ask or allow per the existing remembered scopes and config lists.
	ApprovalAuto ApprovalMode = "Auto"
	// ApprovalFull allows every tool unconditionally, equivalent to --yolo.
	ApprovalFull ApprovalMode = "Full"
)

// readClassTools is the allowlist of tools considered read-only (no writes or execution).
// It is a package var so it can be adjusted without touching the resolution logic.
var readClassTools = map[string]bool{
	"view":          true,
	"ls":            true,
	"grep":          true,
	"glob":          true,
	"diagnostics":   true,
	"symbols":       true,
	"navigate":      true,
	"mcp_resources": true,
	"mcp_prompts":   true,
	"web_fetch":     true,
	"web_search":    true,
	"job_output":    true,
	"job_list":      true,
	"think":         true,
}

// Decision represents the permission level of a check.
type Decision string

const (
	// DecisionAllow means the execution is approved.
	DecisionAllow Decision = "Allow"
	// DecisionDeny means the execution is blocked.
	DecisionDeny Decision = "Deny"
	// DecisionAllowOnce represents single-time approval.
	DecisionAllowOnce Decision = "AllowOnce"
	// DecisionAllowSession represents approval for the active session.
	DecisionAllowSession Decision = "AllowSession"
	// DecisionAllowProject represents approval persistent to the current project.
	DecisionAllowProject Decision = "AllowProject"
	// DecisionAllowForever represents global perpetual approval.
	DecisionAllowForever Decision = "AllowForever"
)

// Scope controls where a remembered decision is stored.
type Scope string

const (
	// ScopeOnce means only valid for a single execution.
	ScopeOnce Scope = "Once"
	// ScopeSession holds memory for the session duration.
	ScopeSession Scope = "Session"
	// ScopeProject persists to the project's .bharatcode.json.
	ScopeProject Scope = "Project"
	// ScopeForever persists globally to config.json.
	ScopeForever Scope = "Forever"
)

// AuditRecord is an immutable record of a single permission decision, suitable
// for enterprise audit trails. ArgsSummary is the sanitized (secret-redacted,
// length-bounded) rendering of the request arguments produced by sanitizeLogArgs;
// raw secret values are never stored.
type AuditRecord struct {
	Timestamp   time.Time `json:"timestamp"`
	Tool        string    `json:"tool"`
	SessionID   string    `json:"session_id"`
	ArgsSummary string    `json:"args_summary"`
	Decision    Decision  `json:"decision"`
	Scope       Scope     `json:"scope"`
}

// AuditLogger receives one AuditRecord per permission decision. Implementations
// must be safe for concurrent use because Check may be called from many
// goroutines. The default Checker logger is a no-op.
type AuditLogger interface {
	// Log records a single permission decision. It must not retain references to
	// mutable caller state beyond the call.
	Log(ctx context.Context, rec AuditRecord)
}

// noOpAuditLogger discards every record; it is the default so Check never has to
// nil-check the logger.
type noOpAuditLogger struct{}

// Log discards the record.
func (noOpAuditLogger) Log(context.Context, AuditRecord) {}

// InMemoryAuditLogger captures every audit record in memory, guarded for
// concurrent use. It is useful for tests and short-lived inspection.
type InMemoryAuditLogger struct {
	mu      sync.Mutex
	records []AuditRecord
}

// Log appends the record under a mutex so concurrent Checks stay race-free.
func (l *InMemoryAuditLogger) Log(_ context.Context, rec AuditRecord) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.records = append(l.records, rec)
}

// Records returns a copy of the captured records in arrival order.
func (l *InMemoryAuditLogger) Records() []AuditRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]AuditRecord, len(l.records))
	copy(out, l.records)
	return out
}

// SlogAuditLogger writes each audit record to a slog.Logger at info level. It
// emits the sanitized argument summary only, never raw secret values.
type SlogAuditLogger struct {
	logger *slog.Logger
}

// NewSlogAuditLogger builds a SlogAuditLogger; a nil logger falls back to
// slog.Default so the result is always usable.
func NewSlogAuditLogger(logger *slog.Logger) *SlogAuditLogger {
	if logger == nil {
		logger = slog.Default()
	}
	return &SlogAuditLogger{logger: logger}
}

// Log writes the record to the underlying slog.Logger.
func (l *SlogAuditLogger) Log(ctx context.Context, rec AuditRecord) {
	l.logger.InfoContext(
		ctx, "permission audit",
		"timestamp", rec.Timestamp,
		"tool", rec.Tool,
		"session_id", rec.SessionID,
		"args", rec.ArgsSummary,
		"decision", rec.Decision,
		"scope", rec.Scope,
	)
}

// Request defines the context and arguments of a tool execution.
type Request struct {
	ToolName  string
	Args      map[string]any
	SessionID string
}

// ErrCancelled is returned when a permission request blocks on user input and the context is cancelled.
var ErrCancelled = errors.New("permission check cancelled")

// Checker manages gating, allow-lists, and persisted approvals.
type Checker struct {
	mu            sync.RWMutex
	cfg           *config.Config
	bus           *pubsub.Topic[pubsub.PermissionRequest]
	yolo          bool
	approvalMode  ApprovalMode
	auditLogger   AuditLogger
	sessionMemory sync.Map
	projectMemory sync.Map
	globalMemory  sync.Map
}

// New constructs a Checker with the given config and pubsub topic.
func New(cfg *config.Config, bus *pubsub.Topic[pubsub.PermissionRequest]) *Checker {
	c := &Checker{
		cfg:          cfg,
		bus:          bus,
		approvalMode: ApprovalAuto,
		auditLogger:  noOpAuditLogger{},
	}

	// Load project level remembered decisions.
	cwd, err := os.Getwd()
	if err == nil {
		projPath := config.ProjectPath(cwd)
		for k, v := range loadRememberedMap(projPath) {
			c.projectMemory.Store(k, Decision(v))
		}
	}

	// Load global level remembered decisions.
	for k, v := range loadRememberedMap(config.GlobalPath()) {
		c.globalMemory.Store(k, Decision(v))
	}

	return c
}

// loadRememberedMap reads and parses the configuration file at the given path.
func loadRememberedMap(path string) map[string]string {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var tmp config.Config
	if err := json.Unmarshal(data, &tmp); err == nil {
		return tmp.Permissions.Remembered
	}
	return nil
}

// SetYolo turns global YOLO auto-approval mode on or off.
func (c *Checker) SetYolo(on bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.yolo = on
}

// Yolo reports whether global YOLO auto-approval mode is currently on. It is the
// read companion to SetYolo, letting a UI seam show the yolo state without
// tracking the toggles itself. It reflects only the explicit SetYolo flag, not
// the config-level AllowAll fallback that Check also honors, so callers asking
// "is yolo toggled on" get the toggle's value.
func (c *Checker) Yolo() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.yolo
}

// SetApprovalMode sets the global approval-mode policy (ReadOnly, Auto, or Full).
func (c *Checker) SetApprovalMode(mode ApprovalMode) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.approvalMode = mode
}

// GetApprovalMode returns the current global approval-mode policy.
func (c *Checker) GetApprovalMode() ApprovalMode {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.approvalMode
}

// SetAuditLogger installs the audit sink that records every permission decision.
// A nil logger resets the Checker to the no-op default.
func (c *Checker) SetAuditLogger(logger AuditLogger) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if logger == nil {
		logger = noOpAuditLogger{}
	}
	c.auditLogger = logger
}

// Check evaluates the permission request.
//
// Resolution order: yolo -> deny-wins across all scopes -> allow across
// session/project/global -> config deny list -> config auto-approve list ->
// approval mode -> interactive prompt. A stored Deny at any scope is sticky:
// an AllowSession can never override a DenyProject.
func (c *Checker) Check(ctx context.Context, req Request) (decision Decision, err error) {
	// scope records where the resolved decision came from. Memory-hit paths set
	// the actual remembered scope (Session/Project/Forever); every scope-less
	// path (yolo, config lists, approval mode, prompt-once, nil bus, cancel)
	// stays ScopeOnce, meaning "not drawn from a broader remembered scope".
	scope := ScopeOnce

	c.mu.RLock()
	logger := c.auditLogger
	yolo := c.yolo || (c.cfg != nil && c.cfg.Permissions.AllowAll)
	mode := c.approvalMode
	c.mu.RUnlock()

	// Emit exactly one audit record per Check from a single deferred site so no
	// return path can escape the audit trail, including the early yolo bypass and
	// the context-cancelled deny. The argument summary is sanitized so raw secret
	// values never reach the audit sink.
	defer func() {
		logger.Log(ctx, AuditRecord{
			Timestamp:   time.Now().UTC(),
			Tool:        req.ToolName,
			SessionID:   req.SessionID,
			ArgsSummary: sanitizeLogArgs(req.Args),
			Decision:    decision,
			Scope:       scope,
		})
	}()

	// 1. YOLO Check.
	if yolo {
		slog.WarnContext(
			ctx, "Bypassing tool permission check in YOLO mode",
			"tool", req.ToolName,
			"args", sanitizeLogArgs(req.Args),
		)
		return DecisionAllow, nil
	}

	// 2-4. Resolve remembered scopes and config lists. A compound bash command
	// (ls && rm, a | b, $(...), ...) collapses to more than one command head,
	// each of which must clear permission independently — otherwise auto-approving
	// a benign head (bash:ls) would silently approve a chained dangerous tail
	// (rm -rf /). Single commands keep the original single-key resolution exactly.
	if heads, parseComplete, compound := c.bashCompound(req); compound {
		if dec, sc, resolved := c.resolveBashHeads(heads, parseComplete); resolved {
			scope = sc
			return dec, nil
		}
	} else if dec, sc, resolved := c.resolveSingleKey(req.ToolName, c.getMatchKey(req)); resolved {
		scope = sc
		return dec, nil
	}

	// 5. Approval-mode policy, consulted before the interactive prompt.
	switch mode {
	case ApprovalFull:
		return DecisionAllow, nil
	case ApprovalReadOnly:
		if readClassTools[req.ToolName] {
			return DecisionAllow, nil
		}
		return DecisionDeny, nil
	}

	// 6. Fallback to TUI prompt via pubsub.
	if c.bus == nil {
		return DecisionDeny, nil
	}

	replyChan := make(chan pubsub.PermissionDecision, 1)
	pubsubReq := pubsub.PermissionRequest{
		Tool:   req.ToolName,
		Args:   req.Args,
		Reason: fmt.Sprintf("Tool %s needs authorization", req.ToolName),
		Reply:  replyChan,
	}

	c.bus.Publish(ctx, pubsubReq)

	select {
	case <-ctx.Done():
		return DecisionDeny, ErrCancelled
	case dec := <-replyChan:
		var finalDec Decision
		if dec.Approved {
			if dec.Remember {
				finalDec = DecisionAllowSession
				scope = ScopeSession
				_ = c.RememberDecision(req, finalDec, ScopeSession)
			} else {
				finalDec = DecisionAllowOnce
			}
		} else {
			finalDec = DecisionDeny
		}
		return finalDec, nil
	}
}

// RememberDecision stores a decision in the specified scope (session, project, or forever).
func (c *Checker) RememberDecision(req Request, decision Decision, scope Scope) error {
	key := c.getMatchKey(req)
	var mappedDec Decision
	if decision == DecisionAllow || decision == DecisionAllowOnce || decision == DecisionAllowSession || decision == DecisionAllowProject || decision == DecisionAllowForever {
		mappedDec = DecisionAllow
	} else {
		mappedDec = DecisionDeny
	}

	switch scope {
	case ScopeSession:
		c.sessionMemory.Store(key, mappedDec)
		return nil

	case ScopeProject:
		c.projectMemory.Store(key, mappedDec)

		// Persist to project config file.
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getting current directory for project scope persistence: %w", err)
		}
		projPath := config.ProjectPath(cwd)
		if projPath == "" {
			projPath = filepath.Join(cwd, ".bharatcode.json")
		}

		return updateConfigFile(context.Background(), projPath, key, string(mappedDec), config.ScopeProject)

	case ScopeForever:
		c.globalMemory.Store(key, mappedDec)

		// Persist to global config file.
		return updateConfigFile(context.Background(), config.GlobalPath(), key, string(mappedDec), config.ScopeGlobal)

	default:
		return nil
	}
}

// updateConfigFile loads, updates, and saves the configuration atomically at the given path.
func updateConfigFile(ctx context.Context, path, key, val string, scope config.Scope) error {
	var tmp config.Config
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &tmp)
	}

	if tmp.Permissions.Remembered == nil {
		tmp.Permissions.Remembered = make(map[string]string)
	}
	tmp.Permissions.Remembered[key] = val

	return config.Save(ctx, &tmp, scope)
}

// getMatchKey sanitizes arguments and produces the canonical lookup key (e.g. "bash:rm" or "edit:/path").
func (c *Checker) getMatchKey(req Request) string {
	switch req.ToolName {
	case "bash":
		cmd := bashCmdString(req.Args)
		if cmd == "" {
			return "bash"
		}
		if w := firstCommandWord(cmd); w != "" {
			return "bash:" + w
		}
		return "bash"

	case "edit", "write", "view":
		pathRaw, ok := req.Args["path"].(string)
		if !ok {
			pathRaw, ok = req.Args["TargetFile"].(string)
		}
		if !ok {
			pathRaw, ok = req.Args["AbsolutePath"].(string)
		}
		if !ok || pathRaw == "" {
			return req.ToolName
		}
		abs, err := filepath.Abs(pathRaw)
		if err != nil {
			return req.ToolName + ":" + filepath.Clean(pathRaw)
		}
		return req.ToolName + ":" + abs

	case "web_fetch", "web_search":
		urlRaw, ok := req.Args["url"].(string)
		if !ok {
			urlRaw, ok = req.Args["Url"].(string)
		}
		if !ok || urlRaw == "" {
			return req.ToolName
		}
		if !strings.Contains(urlRaw, "://") {
			urlRaw = "http://" + urlRaw
		}
		u, err := url.Parse(urlRaw)
		if err == nil {
			return req.ToolName + ":" + u.Host
		}
		return req.ToolName + ":" + urlRaw

	default:
		return req.ToolName
	}
}

// sanitizeLogArgs strips sensitive or long parameters for logging.
func sanitizeLogArgs(args map[string]any) string {
	if len(args) == 0 {
		return "{}"
	}
	clean := make(map[string]any)
	for k, v := range args {
		vStr := fmt.Sprintf("%v", v)
		if len(vStr) > 100 {
			clean[k] = vStr[:97] + "..."
		} else {
			clean[k] = v
		}
	}
	b, _ := json.Marshal(clean)
	return string(b)
}

// matchPattern evaluates if key matches a config pattern rule.
//
// A match requires either an explicit wildcard ("*" for every tool, or
// "<tool>:*" for every invocation of one tool) or an exact key equality.
// Prefix matching is deliberately avoided so that "bash:echo" never silently
// broadens to "bash:echox" or "bash:echofoo".
func matchPattern(tool, key, pattern string) bool {
	if pattern == "*" || pattern == tool+":*" {
		return true
	}
	return key == pattern
}

// bashCmdString extracts the shell command line from a bash tool request's
// arguments, accepting both the native "cmd" key and the "CommandLine" alias.
func bashCmdString(args map[string]any) string {
	if v, ok := args["cmd"].(string); ok {
		return v
	}
	if v, ok := args["CommandLine"].(string); ok {
		return v
	}
	return ""
}

// firstCommandWord returns the head (the command name) of a single shell
// command segment: the first token that is neither a flag nor punctuation. It
// is the canonical reduction used to key a bash invocation for permission
// matching, e.g. "git" for "git commit -m ...".
func firstCommandWord(cmd string) string {
	for _, w := range strings.Fields(cmd) {
		if strings.HasPrefix(w, "-") {
			continue
		}
		w = strings.Trim(w, "\"'`;|&><")
		if w == "" {
			continue
		}
		return w
	}
	return ""
}

// splitBashSegments splits a command line into its sub-command segments at the
// unquoted shell operators that introduce a new command (";", "&&", "||", "|",
// background "&", and newlines). Quoting is honored so a separator inside a
// quoted string (echo "a; b") does not split. It also reports whether a command
// substitution ($( ... ) or backticks) was seen: such constructs can hide
// further commands that this lightweight splitter does not descend into, so the
// caller treats the parse as incomplete and refuses to auto-approve via narrow
// per-command rules. Over-splitting (treating a separator that bash would have
// grouped as a boundary) is deliberately tolerated because it only makes
// auto-approval stricter, never looser.
func splitBashSegments(cmd string) (segs []string, hasSubstitution bool) {
	var b strings.Builder
	var inSingle, inDouble bool
	flush := func() {
		if s := strings.TrimSpace(b.String()); s != "" {
			segs = append(segs, s)
		}
		b.Reset()
	}
	runes := []rune(cmd)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		switch {
		case inSingle:
			b.WriteRune(c)
			if c == '\'' {
				inSingle = false
			}
		case inDouble:
			// Command substitution stays active inside double quotes, so detect
			// it here too rather than trusting the quote to neutralize it.
			if c == '`' {
				hasSubstitution = true
			} else if c == '$' && i+1 < len(runes) && runes[i+1] == '(' {
				hasSubstitution = true
			}
			b.WriteRune(c)
			if c == '"' {
				inDouble = false
			}
		case c == '\'':
			inSingle = true
			b.WriteRune(c)
		case c == '"':
			inDouble = true
			b.WriteRune(c)
		case c == '`':
			hasSubstitution = true
			b.WriteRune(c)
		case c == '$' && i+1 < len(runes) && runes[i+1] == '(':
			hasSubstitution = true
			b.WriteRune(c)
		case c == ';' || c == '\n':
			flush()
		case c == '&':
			if i+1 < len(runes) && runes[i+1] == '&' {
				i++
			}
			flush()
		case c == '|':
			if i+1 < len(runes) && runes[i+1] == '|' {
				i++
			}
			flush()
		default:
			b.WriteRune(c)
		}
	}
	flush()
	return segs, hasSubstitution
}

// bashHeads reduces a command line to the de-duplicated set of permission keys
// for the command heads it runs ("bash:git", "bash:rm", ...). parseComplete is
// false when a command substitution was seen, signalling that the key set may be
// incomplete and so narrow auto-approval must be withheld.
func bashHeads(cmd string) (heads []string, parseComplete bool) {
	segs, hasSub := splitBashSegments(cmd)
	seen := map[string]bool{}
	for _, s := range segs {
		w := firstCommandWord(s)
		if w == "" {
			continue
		}
		key := "bash:" + w
		if !seen[key] {
			seen[key] = true
			heads = append(heads, key)
		}
	}
	return heads, !hasSub
}

// bashCompound reports whether req is a bash invocation whose command line
// composes more than one command (or hides commands inside a substitution),
// returning the per-head permission keys and whether the parse was complete.
// compound is false for non-bash tools and for a single, fully-parsed command,
// in which case the caller falls back to the original single-key resolution.
func (c *Checker) bashCompound(req Request) (heads []string, parseComplete, compound bool) {
	if req.ToolName != "bash" {
		return nil, true, false
	}
	heads, parseComplete = bashHeads(bashCmdString(req.Args))
	compound = len(heads) > 1 || !parseComplete
	return heads, parseComplete, compound
}

// resolveSingleKey applies the remembered-scope and config-list resolution to a
// single permission key, returning the decision and the scope it came from. The
// resolution order matches the original Check flow exactly: a stored Deny at any
// scope wins, then a stored Allow at the narrowest scope, then the config deny
// list, then the config auto-approve list. resolved is false when no rule
// matched, leaving the decision to the approval mode and interactive prompt.
func (c *Checker) resolveSingleKey(tool, key string) (decision Decision, scope Scope, resolved bool) {
	memScopes := []Scope{ScopeSession, ScopeProject, ScopeForever}
	mems := []*sync.Map{&c.sessionMemory, &c.projectMemory, &c.globalMemory}

	// Deny-wins: a stored Deny at any scope overrides any narrower Allow.
	for i, mem := range mems {
		if v, ok := mem.Load(key); ok && v.(Decision) == DecisionDeny {
			return DecisionDeny, memScopes[i], true
		}
	}
	// Any remaining stored value is an Allow (RememberDecision collapses them).
	for i, mem := range mems {
		if v, ok := mem.Load(key); ok {
			return v.(Decision), memScopes[i], true
		}
	}
	if c.cfg != nil {
		for _, pattern := range c.cfg.Permissions.Deny {
			if matchPattern(tool, key, pattern) {
				return DecisionDeny, ScopeOnce, true
			}
		}
		for _, pattern := range c.cfg.Permissions.AutoApprove {
			if matchPattern(tool, key, pattern) {
				return DecisionAllow, ScopeOnce, true
			}
		}
	}
	return "", ScopeOnce, false
}

// resolveBashHeads resolves a compound bash command against all of its command
// heads. Deny wins: if any head is denied at any scope or by the config deny
// list, the whole command is denied. Allow requires every head to be allowed
// independently (by a remembered Allow or the config auto-approve list); a
// single un-cleared head leaves the command for the interactive prompt. When the
// parse was incomplete (command substitution present), only a blanket bash:* / *
// auto-approve clears the command — narrow per-command rules are insufficient
// because the hidden command's head is not in the key set.
func (c *Checker) resolveBashHeads(heads []string, parseComplete bool) (decision Decision, scope Scope, resolved bool) {
	memScopes := []Scope{ScopeSession, ScopeProject, ScopeForever}
	mems := []*sync.Map{&c.sessionMemory, &c.projectMemory, &c.globalMemory}

	// Deny-wins across every head, memory before config.
	for _, key := range heads {
		for i, mem := range mems {
			if v, ok := mem.Load(key); ok && v.(Decision) == DecisionDeny {
				return DecisionDeny, memScopes[i], true
			}
		}
	}
	if c.cfg != nil {
		for _, key := range heads {
			for _, pattern := range c.cfg.Permissions.Deny {
				if matchPattern("bash", key, pattern) {
					return DecisionDeny, ScopeOnce, true
				}
			}
		}
	}

	if !parseComplete {
		if c.cfg != nil {
			for _, pattern := range c.cfg.Permissions.AutoApprove {
				if pattern == "*" || pattern == "bash:*" {
					return DecisionAllow, ScopeOnce, true
				}
			}
		}
		return "", ScopeOnce, false
	}

	if len(heads) == 0 {
		return "", ScopeOnce, false
	}
	scope = ScopeOnce
	for _, key := range heads {
		allowed := false
		for i, mem := range mems {
			if v, ok := mem.Load(key); ok && v.(Decision) == DecisionAllow {
				allowed = true
				if scope == ScopeOnce {
					scope = memScopes[i]
				}
				break
			}
		}
		if !allowed && c.cfg != nil {
			for _, pattern := range c.cfg.Permissions.AutoApprove {
				if matchPattern("bash", key, pattern) {
					allowed = true
					break
				}
			}
		}
		if !allowed {
			return "", ScopeOnce, false
		}
	}
	return DecisionAllow, scope, true
}
