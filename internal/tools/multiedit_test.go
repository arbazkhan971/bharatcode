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
	// Record a read so the read-before-edit guard is satisfied (a real session
	// reaches multiedit via the view tool, which records the read).
	require.NoError(t, tracker.RecordRead(ctx, "multiedit-records", path))
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

// TestMultiEditNotFoundShowsNearMatchHint verifies that when an edit's old value
// is absent but a whitespace-normalised version exists in the file, the error
// message includes a near-match hint so the model can correct its indentation.
func TestMultiEditNotFoundShowsNearMatchHint(t *testing.T) {
	workDir := t.TempDir()
	path := filepath.Join(workDir, "indent.txt")
	// File uses four-space indentation; the model provides tab-indented text.
	// Neither is a substring of the other, but they normalise identically.
	require.NoError(t, os.WriteFile(path, []byte("    func hello() {}\n"), 0o644))

	tool := newMultiEditTool(Dependencies{WorkDir: workDir, SessionID: "multiedit-hint"})
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "indent.txt",
		"edits": []map[string]any{
			{"old": "\tfunc hello() {}", "new": "\tfunc namaste() {}"}, // tab vs spaces
		},
	}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content, "edits[0].old was not found")
	// The hint must mention whitespace so the model can recover.
	require.Contains(t, result.Content, "whitespace")
	// File must be untouched.
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "    func hello() {}\n", string(got))
}

// TestMultiEditRejectsStaleRead verifies that a multiedit is rejected when the
// file changed on disk since the session last read it, with a clear re-view
// instruction, and the file is left unmodified.
func TestMultiEditRejectsStaleRead(t *testing.T) {
	ctx := context.Background()
	workDir := t.TempDir()
	path := filepath.Join(workDir, "stale.txt")
	require.NoError(t, os.WriteFile(path, []byte("alpha beta\n"), 0o644))

	tracker := newToolsTestTracker(t, "multiedit-stale")
	require.NoError(t, tracker.RecordRead(ctx, "multiedit-stale", path))

	// External modification after the recorded read.
	require.NoError(t, os.WriteFile(path, []byte("alpha beta gamma\n"), 0o644))

	tool := newMultiEditTool(Dependencies{
		FileTracker: tracker,
		WorkDir:     workDir,
		SessionID:   "multiedit-stale",
	})
	result, err := tool.Run(ctx, mustJSON(t, map[string]any{
		"path": "stale.txt",
		"edits": []map[string]any{
			{"old": "alpha beta gamma", "new": "replaced"},
		},
	}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content, "modified on disk")
	require.Contains(t, result.Content, "view")

	// File must remain exactly as the external edit left it.
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "alpha beta gamma\n", string(got))
}

// TestMultiEditStaleReadSkippedWhenNoTracker verifies the guard is a no-op when
// FileTracker is nil.
func TestMultiEditStaleReadSkippedWhenNoTracker(t *testing.T) {
	ctx := context.Background()
	workDir := t.TempDir()
	path := filepath.Join(workDir, "notrace.txt")
	require.NoError(t, os.WriteFile(path, []byte("alpha beta\n"), 0o644))

	tool := newMultiEditTool(Dependencies{WorkDir: workDir, SessionID: "multiedit-notrace"})
	result, err := tool.Run(ctx, mustJSON(t, map[string]any{
		"path": "notrace.txt",
		"edits": []map[string]any{
			{"old": "alpha", "new": "one"},
		},
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "one beta\n", string(got))
}

// TestMultiEditRejectsUnviewedFile verifies the read-before-edit guard applies
// to multiedit too: editing an existing file the tracked session never read is
// refused, and the file is left untouched.
func TestMultiEditRejectsUnviewedFile(t *testing.T) {
	ctx := context.Background()
	workDir := t.TempDir()
	path := filepath.Join(workDir, "unviewed.txt")
	require.NoError(t, os.WriteFile(path, []byte("alpha beta\n"), 0o644))

	tracker := newToolsTestTracker(t, "multiedit-unviewed")
	tool := newMultiEditTool(Dependencies{
		FileTracker: tracker,
		WorkDir:     workDir,
		SessionID:   "multiedit-unviewed",
	})

	result, err := tool.Run(ctx, mustJSON(t, map[string]any{
		"path": "unviewed.txt",
		"edits": []map[string]any{
			{"old": "alpha", "new": "one"},
		},
	}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content, "has not been read in this session")

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "alpha beta\n", string(got))
}

func TestMultiEditResultIncludesUnifiedDiff(t *testing.T) {
	workDir := t.TempDir()
	path := filepath.Join(workDir, "multi.txt")
	require.NoError(t, os.WriteFile(path, []byte("alpha\nbeta\ngamma\n"), 0o644))

	tool := newMultiEditTool(Dependencies{WorkDir: workDir, SessionID: "multiedit-diff"})
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "multi.txt",
		"edits": []map[string]any{
			{"old": "alpha", "new": "ALPHA"},
			{"old": "gamma", "new": "GAMMA"},
		},
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Contains(t, result.Content, "@@")
	require.Contains(t, result.Content, "-alpha")
	require.Contains(t, result.Content, "+ALPHA")
	require.Contains(t, result.Content, "-gamma")
	require.Contains(t, result.Content, "+GAMMA")
	require.NotEmpty(t, result.Metadata["diff"])
}
