package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/db"
	"github.com/arbazkhan971/bharatcode/internal/db/sqlc"
	"github.com/arbazkhan971/bharatcode/internal/filetracker"
	"github.com/stretchr/testify/require"
)

func TestViewRecordsReadAndNumbersLines(t *testing.T) {
	ctx := context.Background()
	workDir := t.TempDir()
	path := filepath.Join(workDir, "main.go")
	require.NoError(t, os.WriteFile(path, []byte("package main\n\nfunc main() {}\n"), 0o644))

	tracker := newToolsTestTracker(t, "view-records")
	tool := newViewTool(Dependencies{
		FileTracker: tracker,
		WorkDir:     workDir,
		SessionID:   "view-records",
	})

	result, err := tool.Run(ctx, mustJSON(t, map[string]any{
		"path":   "main.go",
		"offset": 1,
		"limit":  2,
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Contains(t, result.Content, "1 | package main")
	require.Contains(t, result.Content, "2 | ")

	conflict, err := tracker.HasConflict(ctx, "view-records", path)
	require.NoError(t, err)
	require.False(t, conflict)
}

func TestViewRejectsPathOutsideWorkDir(t *testing.T) {
	workDir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	require.NoError(t, os.WriteFile(outside, []byte("secret"), 0o644))

	tool := newViewTool(Dependencies{WorkDir: workDir, SessionID: "view-outside"})
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]string{"path": outside}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content, "outside the workspace")
}

func TestViewMalformedArgs(t *testing.T) {
	tool := newViewTool(Dependencies{WorkDir: t.TempDir(), SessionID: "view-bad"})
	result, err := tool.Run(context.Background(), []byte(`{`))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content, "invalid JSON arguments")
}

func newToolsTestTracker(t *testing.T, sessionID string) *filetracker.Tracker {
	t.Helper()

	database, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "tools.db"))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, database.Close())
	})

	_, err = database.Queries.CreateSession(context.Background(), sqlc.CreateSessionParams{
		ID:          sessionID,
		ProjectPath: t.TempDir(),
		Title:       "Tools Test",
		Model:       "test-model",
		Agent:       "test-agent",
		CreatedAt:   time.Now().UnixMilli(),
		UpdatedAt:   time.Now().UnixMilli(),
	})
	require.NoError(t, err)

	return filetracker.NewTracker(database, nil)
}
