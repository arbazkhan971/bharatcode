// Package permission implements gating controls and user validation.
package permission_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/permission"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/stretchr/testify/require"
)

func TestPermission_YoloBypass(t *testing.T) {
	bus := pubsub.NewTopic[pubsub.PermissionRequest]("test_permission_yolo", 16)
	defer bus.Close()

	cfg := &config.Config{}
	checker := permission.New(cfg, bus)
	checker.SetYolo(true)

	req := permission.Request{
		ToolName: "bash",
		Args:     map[string]any{"cmd": "rm -rf /"},
	}

	dec, err := checker.Check(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, permission.DecisionAllow, dec)
}

func TestPermission_AllowDenyLists(t *testing.T) {
	bus := pubsub.NewTopic[pubsub.PermissionRequest]("test_permission_lists", 16)
	defer bus.Close()

	cfg := &config.Config{
		Permissions: config.PermConfig{
			AutoApprove: []string{"bash:echo"},
			Deny:        []string{"bash:rm"},
		},
	}
	checker := permission.New(cfg, bus)

	// Approved by auto_approve
	dec, err := checker.Check(context.Background(), permission.Request{
		ToolName: "bash",
		Args:     map[string]any{"cmd": "echo hello"},
	})
	require.NoError(t, err)
	require.Equal(t, permission.DecisionAllow, dec)

	// Blocked by deny
	dec2, err2 := checker.Check(context.Background(), permission.Request{
		ToolName: "bash",
		Args:     map[string]any{"cmd": "rm -rf /"},
	})
	require.NoError(t, err2)
	require.Equal(t, permission.DecisionDeny, dec2)
}

func TestPermission_MemoryScopes(t *testing.T) {
	// Set XDG_CONFIG_HOME to a temporary directory so we don't overwrite real user global settings.
	tempHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tempHome)

	// Change working directory to a temporary project directory.
	tempProj := t.TempDir()
	origWd, err := os.Getwd()
	require.NoError(t, err)
	err = os.Chdir(tempProj)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = os.Chdir(origWd)
	})

	bus := pubsub.NewTopic[pubsub.PermissionRequest]("test_permission_scopes", 16)
	defer bus.Close()

	cfg := &config.Config{}
	checker := permission.New(cfg, bus)

	req := permission.Request{
		ToolName: "bash",
		Args:     map[string]any{"cmd": "ls"},
	}

	// 1. Session scope
	err = checker.RememberDecision(req, permission.DecisionAllow, permission.ScopeSession)
	require.NoError(t, err)
	dec, err := checker.Check(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, permission.DecisionAllow, dec)

	// 2. Project scope
	req2 := permission.Request{
		ToolName: "edit",
		Args:     map[string]any{"path": "main.go"},
	}
	err = checker.RememberDecision(req2, permission.DecisionAllow, permission.ScopeProject)
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(tempProj, ".bharatcode.json"))

	// 3. Global scope
	req3 := permission.Request{
		ToolName: "web_fetch",
		Args:     map[string]any{"url": "https://google.com"},
	}
	err = checker.RememberDecision(req3, permission.DecisionDeny, permission.ScopeForever)
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(tempHome, "bharatcode", "config.json"))
}

func TestPermission_AskPromptAndCancellation(t *testing.T) {
	bus := pubsub.NewTopic[pubsub.PermissionRequest]("test_permission_ask", 16)
	defer bus.Close()

	cfg := &config.Config{}
	checker := permission.New(cfg, bus)

	req := permission.Request{
		ToolName: "bash",
		Args:     map[string]any{"cmd": "echo test"},
	}

	// Test 1: User approves and chooses to remember.
	ch, cancelSub := bus.Subscribe()
	defer cancelSub()
	go func() {
		select {
		case pubReq, ok := <-ch:
			if ok {
				pubReq.Reply <- pubsub.PermissionDecision{
					Approved: true,
					Remember: true,
				}
			}
		case <-time.After(1 * time.Second):
		}
	}()

	dec, err := checker.Check(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, permission.DecisionAllowSession, dec)

	// Test 2: Context is cancelled before user replies.
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		// Cancel context after a small delay.
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	req2 := permission.Request{
		ToolName: "bash",
		Args:     map[string]any{"cmd": "whoami"},
	}

	dec2, err2 := checker.Check(ctx, req2)
	require.ErrorIs(t, err2, permission.ErrCancelled)
	require.Equal(t, permission.DecisionDeny, dec2)
}

// isolateConfigDirs redirects project and global config writes to temp dirs so
// RememberDecision persistence never touches the real repo or user config.
func isolateConfigDirs(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	tempProj := t.TempDir()
	origWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tempProj))
	t.Cleanup(func() {
		_ = os.Chdir(origWd)
	})
}

// TestPermission_DenyStickiness asserts a Deny stored at any scope overrides a
// later Allow stored at a narrower scope: an AllowSession cannot undo a DenyProject.
func TestPermission_DenyStickiness(t *testing.T) {
	isolateConfigDirs(t)

	bus := pubsub.NewTopic[pubsub.PermissionRequest]("test_permission_deny_sticky", 16)
	defer bus.Close()

	checker := permission.New(&config.Config{}, bus)

	req := permission.Request{
		ToolName: "bash",
		Args:     map[string]any{"cmd": "rm -rf /tmp/x"},
	}

	// Project scope says Deny.
	require.NoError(t, checker.RememberDecision(req, permission.DecisionDeny, permission.ScopeProject))
	// Session scope then tries to Allow the same key.
	require.NoError(t, checker.RememberDecision(req, permission.DecisionAllow, permission.ScopeSession))

	// Deny must win even though the session-scope Allow was stored last and is
	// resolved earlier in the allow ordering.
	dec, err := checker.Check(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, permission.DecisionDeny, dec, "project-scope Deny must override session-scope Allow")
}

// TestPermission_PrefixOverMatch asserts auto-approve and deny patterns match
// exactly (or via explicit wildcards) and never broaden a key by prefix.
func TestPermission_PrefixOverMatch(t *testing.T) {
	tests := []struct {
		name        string
		autoApprove []string
		deny        []string
		toolName    string
		cmd         string
		want        permission.Decision
	}{
		{
			name:        "exact key auto-approves",
			autoApprove: []string{"bash:echo"},
			toolName:    "bash",
			cmd:         "echo hello",
			want:        permission.DecisionAllow,
		},
		{
			name:        "prefix-extended key does not auto-approve and is denied without bus",
			autoApprove: []string{"bash:echo"},
			toolName:    "bash",
			cmd:         "echox hello",
			want:        permission.DecisionDeny,
		},
		{
			name:        "prefix-extended key does not auto-approve (echofoo)",
			autoApprove: []string{"bash:echo"},
			toolName:    "bash",
			cmd:         "echofoo bar",
			want:        permission.DecisionDeny,
		},
		{
			name:        "tool wildcard matches any invocation",
			autoApprove: []string{"bash:*"},
			toolName:    "bash",
			cmd:         "anything goes",
			want:        permission.DecisionAllow,
		},
		{
			name:        "global wildcard matches",
			autoApprove: []string{"*"},
			toolName:    "bash",
			cmd:         "rm -rf /",
			want:        permission.DecisionAllow,
		},
		{
			// Wildcard auto-approve is an Allow backdrop so that an observed Deny
			// can only come from the deny list firing, not from the fallback.
			name:        "deny exact key blocks despite wildcard allow",
			deny:        []string{"bash:rm"},
			autoApprove: []string{"*"},
			toolName:    "bash",
			cmd:         "rm -rf /",
			want:        permission.DecisionDeny,
		},
		{
			// Under the buggy HasPrefix matcher "bash:rmdir" hit "bash:rm" and
			// returned Deny; with exact matching it falls through to the wildcard
			// auto-approve, so an Allow here proves the deny list no longer over-matches.
			name:        "deny does not over-match by prefix so wildcard allow wins",
			deny:        []string{"bash:rm"},
			autoApprove: []string{"*"},
			toolName:    "bash",
			cmd:         "rmdir foo",
			want:        permission.DecisionAllow,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// nil bus so the fallback is a deterministic Deny rather than a
			// blocking prompt; this lets us distinguish "auto-allowed" from
			// "fell through".
			cfg := &config.Config{
				Permissions: config.PermConfig{
					AutoApprove: tc.autoApprove,
					Deny:        tc.deny,
				},
			}
			checker := permission.New(cfg, nil)

			dec, err := checker.Check(context.Background(), permission.Request{
				ToolName: tc.toolName,
				Args:     map[string]any{"cmd": tc.cmd},
			})
			require.NoError(t, err)
			require.Equal(t, tc.want, dec)
		})
	}
}

// TestPermission_ApprovalModes asserts ReadOnly denies write/execute tools but
// allows read-class tools, and Full allows everything.
func TestPermission_ApprovalModes(t *testing.T) {
	tests := []struct {
		name     string
		mode     permission.ApprovalMode
		toolName string
		args     map[string]any
		want     permission.Decision
	}{
		{
			name:     "read-only denies bash",
			mode:     permission.ApprovalReadOnly,
			toolName: "bash",
			args:     map[string]any{"cmd": "ls"},
			want:     permission.DecisionDeny,
		},
		{
			name:     "read-only denies edit",
			mode:     permission.ApprovalReadOnly,
			toolName: "edit",
			args:     map[string]any{"path": "main.go"},
			want:     permission.DecisionDeny,
		},
		{
			name:     "read-only denies write",
			mode:     permission.ApprovalReadOnly,
			toolName: "write",
			args:     map[string]any{"path": "out.txt"},
			want:     permission.DecisionDeny,
		},
		{
			name:     "read-only allows view",
			mode:     permission.ApprovalReadOnly,
			toolName: "view",
			args:     map[string]any{"path": "main.go"},
			want:     permission.DecisionAllow,
		},
		{
			name:     "read-only allows grep",
			mode:     permission.ApprovalReadOnly,
			toolName: "grep",
			args:     map[string]any{},
			want:     permission.DecisionAllow,
		},
		{
			name:     "full allows bash",
			mode:     permission.ApprovalFull,
			toolName: "bash",
			args:     map[string]any{"cmd": "rm -rf /"},
			want:     permission.DecisionAllow,
		},
		{
			name:     "full allows edit",
			mode:     permission.ApprovalFull,
			toolName: "edit",
			args:     map[string]any{"path": "main.go"},
			want:     permission.DecisionAllow,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// nil bus: in Auto mode this would deny via fallback, so any Allow
			// observed here must come from the approval-mode branch itself.
			checker := permission.New(&config.Config{}, nil)
			checker.SetApprovalMode(tc.mode)
			require.Equal(t, tc.mode, checker.GetApprovalMode())

			dec, err := checker.Check(context.Background(), permission.Request{
				ToolName: tc.toolName,
				Args:     tc.args,
			})
			require.NoError(t, err)
			require.Equal(t, tc.want, dec)
		})
	}
}

// TestPermission_DefaultApprovalModeIsAuto asserts New defaults to Auto so the
// interactive prompt path remains reachable and the zero value never denies silently.
func TestPermission_DefaultApprovalModeIsAuto(t *testing.T) {
	checker := permission.New(&config.Config{}, nil)
	require.Equal(t, permission.ApprovalAuto, checker.GetApprovalMode())
}
