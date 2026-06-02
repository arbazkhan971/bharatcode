//go:build darwin

package shell

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDarwinArgvShape asserts the sandbox-exec argv wraps bash correctly. This
// is a pure string test: darwinArgv touches neither PATH nor the filesystem.
func TestDarwinArgvShape(t *testing.T) {
	argv := darwinArgv("(version 1)\n(allow default)\n", "echo hi")
	require.Equal(t, "sandbox-exec", argv[0])
	require.Equal(t, "-p", argv[1])
	require.Equal(t, "(version 1)\n(allow default)\n", argv[2])
	require.Equal(t, []string{"bash", "-c", "echo hi"}, argv[3:])
}

// TestSeatbeltProfile_WorkspaceWrite asserts the generated profile denies
// network and writes, then re-allows writes only under the (symlink-resolved)
// workspace and temp dir.
func TestSeatbeltProfile_WorkspaceWrite(t *testing.T) {
	ws := t.TempDir()
	tmp := t.TempDir()
	profile := buildSeatbeltProfile(SandboxWorkspaceWrite, ws, tmp)

	require.Contains(t, profile, "(allow default)")
	require.Contains(t, profile, "(deny network*)")
	require.Contains(t, profile, "(deny file-write*)")

	// The workspace and temp dir must each appear as an allowed write subpath.
	// Paths are canonicalised, so compare against the resolved forms.
	for _, p := range canonicalWritePaths(ws, tmp) {
		require.Contains(t, profile, "(allow file-write* (subpath "+seatbeltQuote(p)+"))",
			"profile should permit writes under %s", p)
	}
}

// TestSeatbeltProfile_ReadOnly asserts read-only denies all writes and grants
// no workspace write subpath, but keeps the minimal device carve-out.
func TestSeatbeltProfile_ReadOnly(t *testing.T) {
	ws := t.TempDir()
	tmp := t.TempDir()
	profile := buildSeatbeltProfile(SandboxReadOnly, ws, tmp)

	require.Contains(t, profile, "(deny network*)")
	require.Contains(t, profile, "(deny file-write*)")
	// Device carve-out is present so bash can run.
	require.Contains(t, profile, `(literal "/dev/null")`)
	// No workspace write subpath in read-only mode.
	require.NotContains(t, profile, "(subpath "+seatbeltQuote(canonicalWritePaths(ws, tmp)[0])+")")
}

// TestSeatbeltQuote_Escaping asserts paths with quotes/backslashes cannot
// break out of the profile string literal.
func TestSeatbeltQuote_Escaping(t *testing.T) {
	got := seatbeltQuote(`/tmp/a"b\c`)
	require.Equal(t, `"/tmp/a\"b\\c"`, got)
}

// TestWrapCommand_WorkspaceWriteArgv asserts that when sandbox-exec is present
// (the macOS norm) the argv is the sandbox-exec wrapper, not plain bash.
func TestWrapCommand_WorkspaceWriteArgv(t *testing.T) {
	argv := wrapCommand(SandboxWorkspaceWrite, t.TempDir(), "echo hi")
	if argv[0] == "bash" {
		t.Skip("sandbox-exec not available; wrapCommand degraded to plain bash")
	}
	require.Equal(t, "sandbox-exec", argv[0])
	require.Equal(t, "-p", argv[1])
	require.True(t, strings.Contains(argv[2], "(deny network*)"))
	require.Equal(t, []string{"bash", "-c", "echo hi"}, argv[len(argv)-3:])
}
