//go:build linux

package shell

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestBwrapArgv_WorkspaceWrite asserts the bubblewrap argv binds the host
// read-only, re-binds the workspace and temp dir read-write, unshares the
// network, and ends with the bash invocation. Pure string test (bwrapArgv
// only resolves symlinks, no PATH lookup).
func TestBwrapArgv_WorkspaceWrite(t *testing.T) {
	ws := t.TempDir()
	tmp := t.TempDir()
	argv := bwrapArgv(SandboxWorkspaceWrite, ws, tmp, "echo hi")

	require.Equal(t, "bwrap", argv[0])
	require.True(t, containsSubslice(argv, []string{"--ro-bind", "/", "/"}), "host should be read-only bound")
	require.True(t, containsSubslice(argv, []string{"--unshare-net"}), "network should be unshared")
	require.True(t, containsSubslice(argv, []string{"--die-with-parent"}))

	// Workspace and temp dir each get a rw bind, using canonicalised paths.
	for _, p := range canonicalWritePaths(ws, tmp) {
		require.True(t, containsSubslice(argv, []string{"--bind", p, p}),
			"workspace-write should rw-bind %s", p)
	}

	require.Equal(t, []string{"bash", "-c", "echo hi"}, argv[len(argv)-3:])
}

// TestBwrapArgv_ReadOnly asserts read-only mode adds no rw binds: the only
// writable surfaces are the tmpfs /dev and /proc bwrap provides.
func TestBwrapArgv_ReadOnly(t *testing.T) {
	ws := t.TempDir()
	tmp := t.TempDir()
	argv := bwrapArgv(SandboxReadOnly, ws, tmp, "ls")

	require.Equal(t, "bwrap", argv[0])
	require.True(t, containsSubslice(argv, []string{"--ro-bind", "/", "/"}))
	require.True(t, containsSubslice(argv, []string{"--unshare-net"}))
	require.False(t, containsSubslice(argv, []string{"--bind"}), "read-only must not rw-bind any path")
	require.Equal(t, []string{"bash", "-c", "ls"}, argv[len(argv)-3:])
}
