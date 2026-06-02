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
	"view":        true,
	"ls":          true,
	"grep":        true,
	"glob":        true,
	"diagnostics": true,
	"web_fetch":   true,
	"web_search":  true,
	"job_output":  true,
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

// Check evaluates the permission request.
//
// Resolution order: yolo -> deny-wins across all scopes -> allow across
// session/project/global -> config deny list -> config auto-approve list ->
// approval mode -> interactive prompt. A stored Deny at any scope is sticky:
// an AllowSession can never override a DenyProject.
func (c *Checker) Check(ctx context.Context, req Request) (Decision, error) {
	c.mu.RLock()
	yolo := c.yolo || (c.cfg != nil && c.cfg.Permissions.AllowAll)
	mode := c.approvalMode
	c.mu.RUnlock()

	// 1. YOLO Check.
	if yolo {
		slog.WarnContext(
			ctx, "Bypassing tool permission check in YOLO mode",
			"tool", req.ToolName,
			"args", sanitizeLogArgs(req.Args),
		)
		return DecisionAllow, nil
	}

	key := c.getMatchKey(req)

	// 2. Deny-wins pass: a stored Deny at ANY scope is sticky and overrides
	// any Allow stored at a narrower scope.
	for _, mem := range []*sync.Map{&c.sessionMemory, &c.projectMemory, &c.globalMemory} {
		if val, ok := mem.Load(key); ok && val.(Decision) == DecisionDeny {
			return DecisionDeny, nil
		}
	}

	// 3. Allow resolution in session -> project -> global order. Stored values
	// are collapsed to Allow/Deny by RememberDecision, and Deny was already
	// handled above, so any remaining stored value is an Allow.
	for _, mem := range []*sync.Map{&c.sessionMemory, &c.projectMemory, &c.globalMemory} {
		if val, ok := mem.Load(key); ok {
			return val.(Decision), nil
		}
	}

	// 4. Config-defined allow/deny lists.
	if c.cfg != nil {
		// Deny-list wins first.
		for _, pattern := range c.cfg.Permissions.Deny {
			if matchPattern(req.ToolName, key, pattern) {
				return DecisionDeny, nil
			}
		}

		// Auto-approve lists.
		for _, pattern := range c.cfg.Permissions.AutoApprove {
			if matchPattern(req.ToolName, key, pattern) {
				return DecisionAllow, nil
			}
		}
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
		cmdRaw, ok := req.Args["cmd"].(string)
		if !ok {
			cmdRaw, ok = req.Args["CommandLine"].(string)
		}
		if !ok || cmdRaw == "" {
			return "bash"
		}
		words := strings.Fields(cmdRaw)
		for _, w := range words {
			if strings.HasPrefix(w, "-") {
				continue
			}
			w = strings.Trim(w, "\"'`;|&><")
			if w == "" {
				continue
			}
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
