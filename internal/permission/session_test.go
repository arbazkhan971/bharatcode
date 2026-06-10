package permission_test

import (
	"context"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/permission"
	"github.com/stretchr/testify/require"
)

// TestPermission_SessionGrantDoesNotLeak verifies that a session-scope Allow
// remembered for one session does not auto-approve the same tool/path in a
// different session: with no interactive bus wired, the other session falls
// through to the default deny rather than resolving from the first session's
// memory.
func TestPermission_SessionGrantDoesNotLeak(t *testing.T) {
	checker := permission.New(&config.Config{}, nil)

	reqA := permission.Request{
		ToolName:  "edit",
		Args:      map[string]any{"path": "main.go"},
		SessionID: "session-A",
	}
	require.NoError(t, checker.RememberDecision(reqA, permission.DecisionAllow, permission.ScopeSession))

	// Same tool and path, but a different session.
	reqB := permission.Request{
		ToolName:  "edit",
		Args:      map[string]any{"path": "main.go"},
		SessionID: "session-B",
	}

	// Session A resolves to Allow from its own remembered grant.
	decA, err := checker.Check(context.Background(), reqA)
	require.NoError(t, err)
	require.Equal(t, permission.DecisionAllow, decA)

	// Session B must NOT inherit A's grant; with no bus it falls through to deny.
	decB, err := checker.Check(context.Background(), reqB)
	require.NoError(t, err)
	require.Equal(t, permission.DecisionDeny, decB, "a session-scope grant must not leak across sessions")
}

// TestPermission_SessionGrantCaching verifies that once a session grant is
// remembered, repeated checks for that session resolve from the cache without an
// interactive bus, and that a different path in the same session is not covered.
func TestPermission_SessionGrantCaching(t *testing.T) {
	checker := permission.New(&config.Config{}, nil)

	granted := permission.Request{
		ToolName:  "edit",
		Args:      map[string]any{"path": "a.go"},
		SessionID: "sess-1",
	}
	require.NoError(t, checker.RememberDecision(granted, permission.DecisionAllow, permission.ScopeSession))

	for i := 0; i < 3; i++ {
		dec, err := checker.Check(context.Background(), granted)
		require.NoError(t, err)
		require.Equal(t, permission.DecisionAllow, dec, "cached session grant must resolve every time")
	}

	// A different path in the same session is not covered by the grant.
	other := permission.Request{
		ToolName:  "edit",
		Args:      map[string]any{"path": "b.go"},
		SessionID: "sess-1",
	}
	dec, err := checker.Check(context.Background(), other)
	require.NoError(t, err)
	require.Equal(t, permission.DecisionDeny, dec)
}

// TestPermission_AutoApproveSession verifies the per-session yolo: a session
// marked auto-approve bypasses the prompt for any tool, while other sessions are
// unaffected, and turning it off restores prompting.
func TestPermission_AutoApproveSession(t *testing.T) {
	checker := permission.New(&config.Config{}, nil)

	auto := permission.Request{
		ToolName:  "bash",
		Args:      map[string]any{"cmd": "rm -rf /tmp/x"},
		SessionID: "yolo-session",
	}
	other := permission.Request{
		ToolName:  "bash",
		Args:      map[string]any{"cmd": "rm -rf /tmp/x"},
		SessionID: "normal-session",
	}

	// Before enabling, the auto session still denies (no bus, default deny).
	dec, err := checker.Check(context.Background(), auto)
	require.NoError(t, err)
	require.Equal(t, permission.DecisionDeny, dec)

	require.False(t, checker.IsAutoApproveSession("yolo-session"))
	checker.SetAutoApproveSession("yolo-session", true)
	require.True(t, checker.IsAutoApproveSession("yolo-session"))

	// The auto-approved session now allows everything.
	dec, err = checker.Check(context.Background(), auto)
	require.NoError(t, err)
	require.Equal(t, permission.DecisionAllow, dec)

	// A different session is unaffected — this is per-session, not global.
	dec, err = checker.Check(context.Background(), other)
	require.NoError(t, err)
	require.Equal(t, permission.DecisionDeny, dec)
	require.False(t, checker.Yolo(), "per-session auto-approve must not flip the global yolo flag")

	// Turning it off restores prompting (default deny without a bus).
	checker.SetAutoApproveSession("yolo-session", false)
	require.False(t, checker.IsAutoApproveSession("yolo-session"))
	dec, err = checker.Check(context.Background(), auto)
	require.NoError(t, err)
	require.Equal(t, permission.DecisionDeny, dec)
}

// TestPermission_AutoApproveSession_EmptyID verifies the empty session id is
// never auto-approved and SetAutoApproveSession ignores it.
func TestPermission_AutoApproveSession_EmptyID(t *testing.T) {
	checker := permission.New(&config.Config{}, nil)
	checker.SetAutoApproveSession("", true)
	require.False(t, checker.IsAutoApproveSession(""))
}

// TestPermissionKey_String verifies the canonical key form distinguishes keys
// that differ in any single field.
func TestPermissionKey_String(t *testing.T) {
	base := permission.PermissionKey{SessionID: "s", Tool: "edit", Action: "write", Path: "/a"}
	require.Equal(t, base.String(), permission.PermissionKey{SessionID: "s", Tool: "edit", Action: "write", Path: "/a"}.String())

	variants := []permission.PermissionKey{
		{SessionID: "s2", Tool: "edit", Action: "write", Path: "/a"},
		{SessionID: "s", Tool: "view", Action: "write", Path: "/a"},
		{SessionID: "s", Tool: "edit", Action: "read", Path: "/a"},
		{SessionID: "s", Tool: "edit", Action: "write", Path: "/b"},
	}
	for _, v := range variants {
		require.NotEqual(t, base.String(), v.String())
	}
}
