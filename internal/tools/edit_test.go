package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/filetracker"
	"github.com/stretchr/testify/require"
)

func TestEditReplacesUniqueStringAndRecordsWrite(t *testing.T) {
	ctx := context.Background()
	workDir := t.TempDir()
	path := filepath.Join(workDir, "note.txt")
	require.NoError(t, os.WriteFile(path, []byte("hello BharatCode\n"), 0o644))

	tracker := newToolsTestTracker(t, "edit-records")
	tool := newEditTool(Dependencies{
		FileTracker: tracker,
		WorkDir:     workDir,
		SessionID:   "edit-records",
	})

	result, err := tool.Run(ctx, mustJSON(t, map[string]any{
		"path":       "note.txt",
		"old_string": "hello",
		"new_string": "namaste",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "namaste BharatCode\n", string(got))

	changes, err := tracker.ChangesForSession(ctx, "edit-records")
	require.NoError(t, err)
	require.Len(t, changes, 1)
	require.Equal(t, filetracker.OpEdit, changes[0].Op)
	require.NotEmpty(t, changes[0].BeforeHash)
	require.NotEmpty(t, changes[0].AfterHash)
}

func TestEditRejectsNonUniqueOldString(t *testing.T) {
	workDir := t.TempDir()
	path := filepath.Join(workDir, "dupe.txt")
	require.NoError(t, os.WriteFile(path, []byte("x x\n"), 0o644))

	tool := newEditTool(Dependencies{WorkDir: workDir, SessionID: "edit-dupe"})
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path":       "dupe.txt",
		"old_string": "x",
		"new_string": "y",
	}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content, "Found 2 occurrences of old_string")
	require.Contains(t, result.Content, "must be unique")
	require.Contains(t, result.Content, "more surrounding context")
	require.Contains(t, result.Content, "replace_all")

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "x x\n", string(got))
}

func TestEditNotFoundReportsWhitespaceGuidance(t *testing.T) {
	workDir := t.TempDir()
	path := filepath.Join(workDir, "miss.txt")
	require.NoError(t, os.WriteFile(path, []byte("hello world\n"), 0o644))

	tool := newEditTool(Dependencies{WorkDir: workDir, SessionID: "edit-miss"})
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path":       "miss.txt",
		"old_string": "absent",
		"new_string": "y",
	}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content, "old_string was not found")
	require.Contains(t, result.Content, "whitespace and newlines")

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "hello world\n", string(got))
}

func TestEditMalformedArgs(t *testing.T) {
	tool := newEditTool(Dependencies{WorkDir: t.TempDir(), SessionID: "edit-bad"})
	result, err := tool.Run(context.Background(), []byte(`{`))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content, "invalid JSON arguments")
}
