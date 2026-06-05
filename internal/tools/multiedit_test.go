package tools

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/filetracker"
	"github.com/stretchr/testify/require"
)

func TestMultiEditAppliesSequentialEditsAndRecordsWrite(t *testing.T) {
	ctx := context.Background()
	workDir := t.TempDir()
	path := filepath.Join(workDir, "multi.txt")
	require.NoError(t, os.WriteFile(path, []byte("alpha beta gamma\n"), 0o644))

	tracker := newToolsTestTracker(t, "multiedit-records")
	tool := newMultiEditTool(Dependencies{
		FileTracker: tracker,
		WorkDir:     workDir,
		SessionID:   "multiedit-records",
	})

	result, err := tool.Run(ctx, mustJSON(t, map[string]any{
		"path": "multi.txt",
		"edits": []map[string]any{
			{"old": "alpha", "new": "one"},
			{"old": "gamma", "new": "three"},
		},
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "one beta three\n", string(got))

	changes, err := tracker.ChangesForSession(ctx, "multiedit-records")
	require.NoError(t, err)
	require.Len(t, changes, 1)
	require.Equal(t, filetracker.OpEdit, changes[0].Op)
}

func TestMultiEditFailureLeavesFileUnchanged(t *testing.T) {
	workDir := t.TempDir()
	path := filepath.Join(workDir, "atomic.txt")
	original := []byte("one two three four\n")
	require.NoError(t, os.WriteFile(path, original, 0o644))
	before := sha256.Sum256(original)

	tool := newMultiEditTool(Dependencies{WorkDir: workDir, SessionID: "multiedit-atomic"})
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "atomic.txt",
		"edits": []map[string]any{
			{"old": "one", "new": "1"},
			{"old": "missing", "new": "x"},
			{"old": "three", "new": "3"},
		},
	}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content, "edits[1].old was not found")
	require.Contains(t, result.Content, "whitespace and newlines")

	afterBytes, err := os.ReadFile(path)
	require.NoError(t, err)
	after := sha256.Sum256(afterBytes)
	require.Equal(t, before, after)
	require.Equal(t, string(original), string(afterBytes))
}

func TestMultiEditRejectsNonUniqueOldWithCount(t *testing.T) {
	workDir := t.TempDir()
	path := filepath.Join(workDir, "dupe.txt")
	require.NoError(t, os.WriteFile(path, []byte("x x x\n"), 0o644))

	tool := newMultiEditTool(Dependencies{WorkDir: workDir, SessionID: "multiedit-dupe"})
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "dupe.txt",
		"edits": []map[string]any{
			{"old": "x", "new": "y"},
		},
	}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content, "Found 3 occurrences of edits[0].old")
	require.Contains(t, result.Content, "must be unique")
	require.Contains(t, result.Content, "more surrounding context")
	require.Contains(t, result.Content, "replace_all")

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "x x x\n", string(got))
}

func TestMultiEditMalformedArgs(t *testing.T) {
	tool := newMultiEditTool(Dependencies{WorkDir: t.TempDir(), SessionID: "multiedit-bad"})
	result, err := tool.Run(context.Background(), []byte(`{`))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content, "invalid JSON arguments")
}
