package shell

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestParseSandboxMode covers the config-string to mode mapping, including the
// safe-default fallback for empty/unknown values.
func TestParseSandboxMode(t *testing.T) {
	cases := []struct {
		in   string
		want SandboxMode
	}{
		{"off", SandboxOff},
		{"none", SandboxOff},
		{"workspace-write", SandboxWorkspaceWrite},
		{"workspace_write", SandboxWorkspaceWrite},
		{"read-only", SandboxReadOnly},
		{"readonly", SandboxReadOnly},
		{"full", SandboxFull},
		{"danger-full-access", SandboxFull},
		{"", SandboxWorkspaceWrite},      // empty -> safe default
		{"bogus", SandboxWorkspaceWrite}, // unknown -> safe default
	}
	for _, c := range cases {
		require.Equalf(t, c.want, ParseSandboxMode(c.in), "ParseSandboxMode(%q)", c.in)
	}
}

// TestSandboxModeString checks the round-trippable canonical strings.
func TestSandboxModeString(t *testing.T) {
	require.Equal(t, "off", SandboxOff.String())
	require.Equal(t, "workspace-write", SandboxWorkspaceWrite.String())
	require.Equal(t, "read-only", SandboxReadOnly.String())
	require.Equal(t, "full", SandboxFull.String())
}

// TestSandboxModeConfines documents which modes impose a real boundary.
func TestSandboxModeConfines(t *testing.T) {
	require.False(t, SandboxOff.confines())
	require.False(t, SandboxFull.confines())
	require.True(t, SandboxWorkspaceWrite.confines())
	require.True(t, SandboxReadOnly.confines())
}

// TestWithSandboxMode verifies the constructor option sets the field.
func TestWithSandboxMode(t *testing.T) {
	s := &Shell{}
	WithSandboxMode(SandboxReadOnly)(s)
	require.Equal(t, SandboxReadOnly, s.sandboxMode)
}

// TestWrapCommand_OffAndFullArePlainBash asserts that non-confining modes
// always produce a bare `bash -c <cmd>` argv on every platform.
func TestWrapCommand_OffAndFullArePlainBash(t *testing.T) {
	const cmd = "echo hello"
	for _, mode := range []SandboxMode{SandboxOff, SandboxFull} {
		argv := wrapCommand(mode, t.TempDir(), cmd)
		require.Equal(t, []string{"bash", "-c", cmd}, argv, "mode=%s", mode)
	}
}

// TestWrapCommand_EmptyWorkspaceDegradesToBash asserts workspace-write with no
// cwd falls back to plain bash rather than emitting an empty subpath rule.
func TestWrapCommand_EmptyWorkspaceDegradesToBash(t *testing.T) {
	argv := wrapCommand(SandboxWorkspaceWrite, "", "echo hi")
	require.Equal(t, []string{"bash", "-c", "echo hi"}, argv)
}

// containsSubslice reports whether sub appears contiguously within s.
func containsSubslice(s, sub []string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		match := true
		for j := range sub {
			if s[i+j] != sub[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// TestArgvEndsWithBash asserts every confining-mode argv terminates with the
// bash invocation so the command itself is preserved verbatim.
func TestArgvEndsWithBash(t *testing.T) {
	const cmd = "echo $HOME && ls"
	argv := wrapCommand(SandboxWorkspaceWrite, t.TempDir(), cmd)
	// On a host without the launcher this degrades to plain bash, which still
	// ends with the bash invocation, so the assertion holds either way.
	n := len(argv)
	require.GreaterOrEqual(t, n, 3)
	require.Equal(t, []string{"bash", "-c", cmd}, argv[n-3:])
	require.True(t, strings.Contains(strings.Join(argv, " "), cmd))
}
