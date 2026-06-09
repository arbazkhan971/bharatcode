package cmd

import (
	"bytes"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/selfupdate"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// TestVersionDefaultIsDevSentinel guards against a regression where the
// unstamped default leaks a fake release number (it used to read "v0.2.5").
// An unstamped local build must report the dev sentinel, never a real-looking
// release, so users and the self-update check can tell it apart.
func TestVersionDefaultIsDevSentinel(t *testing.T) {
	require.Equal(t, "v0.0.0", version, "unstamped default version must be the dev sentinel, not a release number")
	require.Equal(t, "0000000", commit, "unstamped default commit must be the dev placeholder")
}

// TestVersionDefaultRecognizedAsUnknown asserts the default version is the same
// dev placeholder selfupdate treats as unknown, so an unstamped build never
// reports an available update.
func TestVersionDefaultRecognizedAsUnknown(t *testing.T) {
	st := selfupdate.CompareVersions(version, "v9.9.9")
	require.False(t, st.UpdateAvailable, "dev-sentinel build must not report an update available")
}

// TestVersionLdflagsOverride verifies the printed version tracks the package
// var, i.e. the -ldflags -X injection path stays intact: overriding version at
// runtime (as the linker does at build time) changes the command's output.
func TestVersionLdflagsOverride(t *testing.T) {
	origVersion, origCommit := version, commit
	t.Cleanup(func() { version, commit = origVersion, origCommit })

	version = "v1.2.3"
	commit = "abcdef0"

	cmd := newVersionCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	require.NoError(t, cmd.RunE(cmd, nil))
	require.Equal(t, "bharatcode v1.2.3 (abcdef0)\n", out.String())
}

// compile-time assurance that newVersionCmd returns a *cobra.Command (keeps the
// test honest if the constructor signature ever drifts).
var _ = func() *cobra.Command { return newVersionCmd() }
