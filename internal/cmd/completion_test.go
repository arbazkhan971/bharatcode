package cmd

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCompletionGeneratesNonEmptyScripts(t *testing.T) {
	cases := []struct {
		shell  string
		needle string
	}{
		{"bash", "# bash completion"},
		{"zsh", "#compdef"},
		{"fish", "complete -c bharatcode"},
	}
	for _, tc := range cases {
		t.Run(tc.shell, func(t *testing.T) {
			stdout, stderr, err := executeRoot(t, "completion", tc.shell)
			require.NoError(t, err)
			require.Empty(t, stderr)
			require.NotEmpty(t, strings.TrimSpace(stdout))
			// The script must mention the binary name and the shell marker
			// so we know it is a real completion script, not a stray string.
			require.Contains(t, stdout, "bharatcode")
			require.Contains(t, stdout, tc.needle)
		})
	}
}

func TestCompletionRejectsUnknownShell(t *testing.T) {
	stdout, stderr, err := executeRoot(t, "completion", "powershell")
	require.Error(t, err)
	require.Empty(t, stdout)
	require.Contains(t, stderr, "unsupported shell")
}

func TestCompletionRequiresExactlyOneArg(t *testing.T) {
	_, _, err := executeRoot(t, "completion")
	require.Error(t, err)
}
