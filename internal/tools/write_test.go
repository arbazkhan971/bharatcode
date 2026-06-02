package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/filetracker"
	"github.com/stretchr/testify/require"
)

func TestWriteCreatesNewFileAndRecordsWrite(t *testing.T) {
	ctx := context.Background()
	workDir := t.TempDir()
	tracker := newToolsTestTracker(t, "write-create")
	tool := newWriteTool(Dependencies{
		FileTracker: tracker,
		WorkDir:     workDir,
		SessionID:   "write-create",
	})

	result, err := tool.Run(ctx, mustJSON(t, map[string]string{
		"path":    "nested/new.txt",
		"content": "created\n",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	got, err := os.ReadFile(filepath.Join(workDir, "nested", "new.txt"))
	require.NoError(t, err)
	require.Equal(t, "created\n", string(got))

	changes, err := tracker.ChangesForSession(ctx, "write-create")
	require.NoError(t, err)
	require.Len(t, changes, 1)
	require.Equal(t, filetracker.OpCreate, changes[0].Op)
}

func TestWriteRefusesExistingUnviewedFile(t *testing.T) {
	workDir := t.TempDir()
	path := filepath.Join(workDir, "existing.txt")
	require.NoError(t, os.WriteFile(path, []byte("original\n"), 0o644))

	tool := newWriteTool(Dependencies{WorkDir: workDir, SessionID: "write-unviewed"})
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]string{
		"path":    "existing.txt",
		"content": "replacement\n",
	}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content, "has not been viewed")

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "original\n", string(got))
}

func TestWriteOverwritesViewedFile(t *testing.T) {
	ctx := context.Background()
	workDir := t.TempDir()
	path := filepath.Join(workDir, "viewed.txt")
	require.NoError(t, os.WriteFile(path, []byte("before\n"), 0o644))

	sessionID := "write-viewed"
	tracker := newToolsTestTracker(t, sessionID)
	view := newViewTool(Dependencies{FileTracker: tracker, WorkDir: workDir, SessionID: sessionID})
	write := newWriteTool(Dependencies{FileTracker: tracker, WorkDir: workDir, SessionID: sessionID})

	viewed, err := view.Run(ctx, mustJSON(t, map[string]string{"path": "viewed.txt"}))
	require.NoError(t, err)
	require.False(t, viewed.IsError)

	result, err := write.Run(ctx, mustJSON(t, map[string]string{
		"path":    "viewed.txt",
		"content": "after\n",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "after\n", string(got))

	changes, err := tracker.ChangesForSession(ctx, sessionID)
	require.NoError(t, err)
	require.Len(t, changes, 1)
	require.Equal(t, filetracker.OpEdit, changes[0].Op)
}

func TestWriteMalformedArgs(t *testing.T) {
	tool := newWriteTool(Dependencies{WorkDir: t.TempDir(), SessionID: "write-bad"})
	result, err := tool.Run(context.Background(), []byte(`{`))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content, "invalid JSON arguments")
}
