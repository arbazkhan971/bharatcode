package cmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHelpListsRevert(t *testing.T) {
	stdout, _, err := executeRoot(t, "--help")
	require.NoError(t, err)
	require.Contains(t, stdout, "revert")
}

func TestRevertNoSessionsErrors(t *testing.T) {
	configPath := writeConfig(t, defaultTestConfig())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	_, _, err := executeRoot(t, "--config", configPath, "--project-dir", t.TempDir(), "revert")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no sessions")
}
