// Package permission implements gating controls and user validation.
package permission_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/permission"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/stretchr/testify/require"
)

// captureLogger is a minimal AuditLogger that records every record it receives.
// It is mutex-guarded because Check may run concurrently and the deferred audit
// emission happens under no external lock.
type captureLogger struct {
	mu      sync.Mutex
	records []permission.AuditRecord
}

func (l *captureLogger) Log(_ context.Context, rec permission.AuditRecord) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.records = append(l.records, rec)
}

func (l *captureLogger) all() []permission.AuditRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]permission.AuditRecord, len(l.records))
	copy(out, l.records)
	return out
}

// TestAudit_RecordsDecisionsAcrossScopes injects a capturing AuditLogger and runs
// several Checks that resolve via distinct paths (yolo allow, config deny, config
// allow, remembered session allow). It asserts each Check produced exactly one
// audit record carrying the right tool, decision, and scope.
func TestAudit_RecordsDecisionsAcrossScopes(t *testing.T) {
	isolateConfigDirs(t)

	bus := pubsub.NewTopic[pubsub.PermissionRequest]("test_audit_scopes", 16)
	defer bus.Close()

	cfg := &config.Config{
		Permissions: config.PermConfig{
			AutoApprove: []string{"bash:echo"},
			Deny:        []string{"bash:rm"},
		},
	}
	checker := permission.New(cfg, bus)

	cap := &captureLogger{}
	checker.SetAuditLogger(cap)

	// Remember a session-scope allow so one Check resolves from session memory.
	sessionReq := permission.Request{
		ToolName: "view",
		Args:     map[string]any{"path": "main.go"},
	}
	require.NoError(t, checker.RememberDecision(sessionReq, permission.DecisionAllow, permission.ScopeSession))

	// Case 1: YOLO bypass -> Allow at ScopeOnce. Toggle yolo only for this Check.
	checker.SetYolo(true)
	yoloReq := permission.Request{
		ToolName:  "bash",
		Args:      map[string]any{"cmd": "rm -rf /"},
		SessionID: "sess-yolo",
	}
	dec, err := checker.Check(context.Background(), yoloReq)
	require.NoError(t, err)
	require.Equal(t, permission.DecisionAllow, dec)
	checker.SetYolo(false)

	// Case 2: config deny list -> Deny at ScopeOnce.
	denyReq := permission.Request{
		ToolName:  "bash",
		Args:      map[string]any{"cmd": "rm -rf /tmp"},
		SessionID: "sess-deny",
	}
	dec, err = checker.Check(context.Background(), denyReq)
	require.NoError(t, err)
	require.Equal(t, permission.DecisionDeny, dec)

	// Case 3: config auto-approve list -> Allow at ScopeOnce.
	allowReq := permission.Request{
		ToolName:  "bash",
		Args:      map[string]any{"cmd": "echo hi"},
		SessionID: "sess-allow",
	}
	dec, err = checker.Check(context.Background(), allowReq)
	require.NoError(t, err)
	require.Equal(t, permission.DecisionAllow, dec)

	// Case 4: remembered session allow -> Allow at ScopeSession.
	dec, err = checker.Check(context.Background(), sessionReq)
	require.NoError(t, err)
	require.Equal(t, permission.DecisionAllow, dec)

	records := cap.all()
	require.Len(t, records, 4, "each Check must emit exactly one audit record")

	type want struct {
		tool     string
		decision permission.Decision
		scope    permission.Scope
	}
	wants := []want{
		{tool: "bash", decision: permission.DecisionAllow, scope: permission.ScopeOnce},    // yolo
		{tool: "bash", decision: permission.DecisionDeny, scope: permission.ScopeOnce},     // config deny
		{tool: "bash", decision: permission.DecisionAllow, scope: permission.ScopeOnce},    // config allow
		{tool: "view", decision: permission.DecisionAllow, scope: permission.ScopeSession}, // session memory
	}
	for i, w := range wants {
		require.Equal(t, w.tool, records[i].Tool, "record %d tool", i)
		require.Equal(t, w.decision, records[i].Decision, "record %d decision", i)
		require.Equal(t, w.scope, records[i].Scope, "record %d scope", i)
		require.False(t, records[i].Timestamp.IsZero(), "record %d timestamp must be set", i)
	}

	// Session ID is propagated into the record for the yolo case.
	require.Equal(t, "sess-yolo", records[0].SessionID)
}

// TestAudit_RedactsLongSecretValues asserts the audit record never contains a raw
// secret value. sanitizeLogArgs redacts by truncating any value longer than 100
// characters, so a 200-char secret must not appear in full in the args summary.
func TestAudit_RedactsLongSecretValues(t *testing.T) {
	secret := strings.Repeat("S", 200)

	cap := &captureLogger{}
	checker := permission.New(&config.Config{}, nil)
	checker.SetAuditLogger(cap)
	checker.SetApprovalMode(permission.ApprovalFull)

	req := permission.Request{
		ToolName:  "bash",
		Args:      map[string]any{"api_key": secret},
		SessionID: "sess-secret",
	}
	dec, err := checker.Check(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, permission.DecisionAllow, dec)

	records := cap.all()
	require.Len(t, records, 1)

	summary := records[0].ArgsSummary
	require.NotContains(t, summary, secret, "raw secret value must not appear in audit record")
	require.Contains(t, summary, "...", "long value must be truncated with an ellipsis")
	// The key may remain; only the full value must be redacted.
	require.Contains(t, summary, "api_key")
}

// TestAudit_ContextCancelledIsAudited asserts the context-cancelled deny path
// still produces an audit record, proving the single deferred emission covers
// even the error return.
func TestAudit_ContextCancelledIsAudited(t *testing.T) {
	bus := pubsub.NewTopic[pubsub.PermissionRequest]("test_audit_cancel", 16)
	defer bus.Close()

	cap := &captureLogger{}
	checker := permission.New(&config.Config{}, bus)
	checker.SetAuditLogger(cap)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before the prompt can be answered

	req := permission.Request{
		ToolName:  "bash",
		Args:      map[string]any{"cmd": "whoami"},
		SessionID: "sess-cancel",
	}
	dec, err := checker.Check(ctx, req)
	require.ErrorIs(t, err, permission.ErrCancelled)
	require.Equal(t, permission.DecisionDeny, dec)

	records := cap.all()
	require.Len(t, records, 1, "cancelled Check must still be audited")
	require.Equal(t, permission.DecisionDeny, records[0].Decision)
	require.Equal(t, permission.ScopeOnce, records[0].Scope)
	require.Equal(t, "sess-cancel", records[0].SessionID)
}

// TestAudit_DefaultLoggerIsNoOp asserts a freshly constructed Checker does not
// panic when Check runs without an injected logger (the default is a no-op).
func TestAudit_DefaultLoggerIsNoOp(t *testing.T) {
	checker := permission.New(&config.Config{}, nil)
	checker.SetApprovalMode(permission.ApprovalFull)

	require.NotPanics(t, func() {
		_, _ = checker.Check(context.Background(), permission.Request{
			ToolName: "bash",
			Args:     map[string]any{"cmd": "ls"},
		})
	})
}

// TestAudit_InMemoryAuditLoggerCaptures exercises the shipped in-memory
// implementation end to end through Check.
func TestAudit_InMemoryAuditLoggerCaptures(t *testing.T) {
	logger := &permission.InMemoryAuditLogger{}
	checker := permission.New(&config.Config{}, nil)
	checker.SetAuditLogger(logger)
	checker.SetApprovalMode(permission.ApprovalReadOnly)

	// view is read-class: allowed under ReadOnly.
	_, err := checker.Check(context.Background(), permission.Request{
		ToolName: "view",
		Args:     map[string]any{"path": "main.go"},
	})
	require.NoError(t, err)

	// bash is not read-class: denied under ReadOnly.
	_, err = checker.Check(context.Background(), permission.Request{
		ToolName: "bash",
		Args:     map[string]any{"cmd": "rm -rf /"},
	})
	require.NoError(t, err)

	records := logger.Records()
	require.Len(t, records, 2)
	require.Equal(t, permission.DecisionAllow, records[0].Decision)
	require.Equal(t, "view", records[0].Tool)
	require.Equal(t, permission.DecisionDeny, records[1].Decision)
	require.Equal(t, "bash", records[1].Tool)
}

// TestAudit_SlogLoggerRedactsAndRecords drives the shipped slog-backed sink
// through Check and asserts the emitted log line records the tool and decision
// while the raw secret value is redacted by truncation before it reaches the sink.
func TestAudit_SlogLoggerRedactsAndRecords(t *testing.T) {
	secret := strings.Repeat("S", 200)

	var buf bytes.Buffer
	sink := permission.NewSlogAuditLogger(slog.New(slog.NewJSONHandler(&buf, nil)))

	checker := permission.New(&config.Config{}, nil)
	checker.SetApprovalMode(permission.ApprovalFull)
	checker.SetAuditLogger(sink)

	_, err := checker.Check(context.Background(), permission.Request{
		ToolName:  "bash",
		Args:      map[string]any{"api_key": secret},
		SessionID: "sess-slog",
	})
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "permission audit")
	require.Contains(t, out, "bash")
	require.Contains(t, out, string(permission.DecisionAllow))
	require.Contains(t, out, "sess-slog")
	require.NotContains(t, out, secret, "raw secret must never reach the slog sink")
}

// TestAudit_NilLoggerResetsToNoOp asserts passing nil to SetAuditLogger restores
// the no-op default rather than leaving a nil interface that would panic.
func TestAudit_NilLoggerResetsToNoOp(t *testing.T) {
	cap := &captureLogger{}
	checker := permission.New(&config.Config{}, nil)
	checker.SetApprovalMode(permission.ApprovalFull)
	checker.SetAuditLogger(cap)
	checker.SetAuditLogger(nil)

	require.NotPanics(t, func() {
		_, _ = checker.Check(context.Background(), permission.Request{
			ToolName: "bash",
			Args:     map[string]any{"cmd": "ls"},
		})
	})
	require.Empty(t, cap.all(), "after reset to no-op, the prior logger must receive nothing")
}
