package cmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAboutCommandPrintsCanonicalIdentity(t *testing.T) {
	stdout, stderr, err := executeRoot(t, "about")
	require.NoError(t, err)
	require.Empty(t, stderr)
	require.Contains(t, stdout, "BharatCode")
	require.Contains(t, stdout, "terminal-based AI coding agent")
	require.NotContains(t, stdout, "I am ChatGPT")
}
