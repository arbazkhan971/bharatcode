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

func TestViewTruncationMarkerIsActionable(t *testing.T) {
	// Render five numbered lines, then cap at a byte budget that only admits
	// the first two so truncation must occur on a line boundary.
	content := "alpha\nbravo\ncharlie\ndelta\necho\n"
	rendered, span := numberedLines(content, 1, 0)
	require.Equal(t, 5, span.total)

	firstLineLen := len("1 | alpha\n")
	out := truncateContent(rendered, span, firstLineLen+1)

	require.Contains(t, out, "1 | alpha")
	require.NotContains(t, out, "3 | charlie")
	// The dead-end byte count must be gone in favour of an offset marker.
	require.NotContains(t, out, "[truncated")
	require.Contains(t, out, "Showing lines 1-1 of 5")
	require.Contains(t, out, "offset=2 to continue")
}

func TestViewOffsetPagesForwardFromMarker(t *testing.T) {
	ctx := context.Background()
	workDir := t.TempDir()
	path := filepath.Join(workDir, "lines.txt")
	require.NoError(t, os.WriteFile(path, []byte("alpha\nbravo\ncharlie\ndelta\necho\n"), 0o644))

	tool := newViewTool(Dependencies{WorkDir: workDir, SessionID: "view-page"})

	// First page: only line one.
	first, err := tool.Run(ctx, mustJSON(t, map[string]any{
		"path":  "lines.txt",
		"limit": 1,
	}))
	require.NoError(t, err)
	require.False(t, first.IsError)
	require.Contains(t, first.Content, "1 | alpha")
	require.NotContains(t, first.Content, "bravo")

	// Continue from the next line, mirroring the offset a truncation marker
	// would advertise.
	second, err := tool.Run(ctx, mustJSON(t, map[string]any{
		"path":   "lines.txt",
		"offset": 2,
		"limit":  1,
	}))
	require.NoError(t, err)
	require.False(t, second.IsError)
	require.Contains(t, second.Content, "2 | bravo")
	require.NotContains(t, second.Content, "alpha")
}

func TestViewTruncationFallsBackForOversizedLine(t *testing.T) {
	// A single line wider than the budget cannot be paged with offset, so the
	// marker must point at a concrete shell fallback instead.
	content := "this single line is far too wide to fit\nsecond\n"
	rendered, span := numberedLines(content, 1, 0)

	out := truncateContent(rendered, span, 12)

	require.Contains(t, out, "exceeds")
	require.Contains(t, out, "view limit")
	require.Contains(t, out, "sed -n")
	require.Contains(t, out, "head -c")
	require.NotContains(t, out, "offset=")
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
