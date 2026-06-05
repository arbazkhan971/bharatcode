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
	// Record a read so the read-before-edit guard is satisfied (a real session
	// reaches edit via the view tool, which records the read).
	require.NoError(t, tracker.RecordRead(ctx, "edit-records", path))
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

// TestEditNotFoundShowsNearMatchHint verifies that when old_string is absent but
// a whitespace-normalised version of it exists in the file, the error message
// includes a near-match hint describing the whitespace/indentation mismatch so
// the model can correct its next attempt.
func TestEditNotFoundShowsNearMatchHint(t *testing.T) {
	workDir := t.TempDir()
	path := filepath.Join(workDir, "indent.txt")
	// File uses four-space indentation; the model provides a tab-indented version.
	// Neither is a substring of the other, but they normalise to the same tokens.
	require.NoError(t, os.WriteFile(path, []byte("    func hello() {}\n"), 0o644))

	tool := newEditTool(Dependencies{WorkDir: workDir, SessionID: "edit-hint"})
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path":       "indent.txt",
		"old_string": "\tfunc hello() {}", // tab-indented — not a substring of the spaces version
		"new_string": "\tfunc namaste() {}",
	}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	// Must report the primary not-found message.
	require.Contains(t, result.Content, "old_string was not found")
	// Must include a near-match / whitespace hint so the model can recover.
	require.Contains(t, result.Content, "whitespace")
	// File must be untouched.
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "    func hello() {}\n", string(got))
}

// TestEditNotFoundShowsClosestRegionHint verifies that when old_string is absent
// but the file contains a line that closely matches the first line of old_string,
// the error message surfaces a numbered context snippet so the model can correct
// its anchor.
func TestEditNotFoundShowsClosestRegionHint(t *testing.T) {
	workDir := t.TempDir()
	path := filepath.Join(workDir, "ctx.txt")
	require.NoError(t, os.WriteFile(path, []byte("line one\nfunc greet(name string)\nline three\n"), 0o644))

	tool := newEditTool(Dependencies{WorkDir: workDir, SessionID: "edit-ctx"})
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path":       "ctx.txt",
		"old_string": "func greet(name string) {}", // slightly different from on-disk
		"new_string": "func greet(name string) { fmt.Println(name) }",
	}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content, "old_string was not found")
	// The hint must show the actual on-disk line (or nearby region).
	require.Contains(t, result.Content, "func greet")
}

// TestEditRejectsStaleRead verifies that an edit is rejected when the file has
// been modified on disk after the session last recorded a read, instructing the
// model to re-view the file before editing.
func TestEditRejectsStaleRead(t *testing.T) {
	ctx := context.Background()
	workDir := t.TempDir()
	path := filepath.Join(workDir, "stale.txt")
	require.NoError(t, os.WriteFile(path, []byte("original content\n"), 0o644))

	tracker := newToolsTestTracker(t, "edit-stale")
	// Record the read so HasConflict has a baseline.
	require.NoError(t, tracker.RecordRead(ctx, "edit-stale", path))

	// Simulate an external modification: overwrite the file on disk.
	require.NoError(t, os.WriteFile(path, []byte("externally modified content\n"), 0o644))

	tool := newEditTool(Dependencies{
		FileTracker: tracker,
		WorkDir:     workDir,
		SessionID:   "edit-stale",
	})
	result, err := tool.Run(ctx, mustJSON(t, map[string]any{
		"path":       "stale.txt",
		"old_string": "externally modified content",
		"new_string": "replaced",
	}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	// The error must tell the model the file changed and it must re-view.
	require.Contains(t, result.Content, "modified on disk")
	require.Contains(t, result.Content, "view")

	// File must remain exactly as the external edit left it.
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "externally modified content\n", string(got))
}

// TestEditStaleReadSkippedWhenNoTracker verifies that the stale-read guard is
// skipped gracefully when FileTracker is nil (tools constructed without one, as
// in simpler tests).
func TestEditStaleReadSkippedWhenNoTracker(t *testing.T) {
	ctx := context.Background()
	workDir := t.TempDir()
	path := filepath.Join(workDir, "notrace.txt")
	require.NoError(t, os.WriteFile(path, []byte("hello\n"), 0o644))

	// No tracker supplied — the guard must not panic or error.
	tool := newEditTool(Dependencies{WorkDir: workDir, SessionID: "edit-notrace"})
	result, err := tool.Run(ctx, mustJSON(t, map[string]any{
		"path":       "notrace.txt",
		"old_string": "hello",
		"new_string": "namaste",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "namaste\n", string(got))
}

// TestEditRejectsUnviewedFile verifies the read-before-edit guard: when a
// tracked session tries to edit an existing file it has never read, the edit is
// refused with guidance to view first, and the file is left untouched.
func TestEditRejectsUnviewedFile(t *testing.T) {
	ctx := context.Background()
	workDir := t.TempDir()
	path := filepath.Join(workDir, "unviewed.txt")
	require.NoError(t, os.WriteFile(path, []byte("hello world\n"), 0o644))

	tracker := newToolsTestTracker(t, "edit-unviewed")
	tool := newEditTool(Dependencies{
		FileTracker: tracker,
		WorkDir:     workDir,
		SessionID:   "edit-unviewed",
	})

	result, err := tool.Run(ctx, mustJSON(t, map[string]any{
		"path":       "unviewed.txt",
		"old_string": "hello",
		"new_string": "namaste",
	}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content, "has not been read in this session")

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "hello world\n", string(got))
}

// TestEditAllowsConsecutiveEditsAfterView verifies that once a file is viewed,
// two edits in a row both succeed: the first write refreshes the read baseline
// so the second edit is not mistaken for a stale-read conflict.
func TestEditAllowsConsecutiveEditsAfterView(t *testing.T) {
	ctx := context.Background()
	workDir := t.TempDir()
	path := filepath.Join(workDir, "seq.txt")
	require.NoError(t, os.WriteFile(path, []byte("alpha beta gamma\n"), 0o644))

	tracker := newToolsTestTracker(t, "edit-seq")
	view := newViewTool(Dependencies{FileTracker: tracker, WorkDir: workDir, SessionID: "edit-seq"})
	tool := newEditTool(Dependencies{FileTracker: tracker, WorkDir: workDir, SessionID: "edit-seq"})

	_, err := view.Run(ctx, mustJSON(t, map[string]any{"path": "seq.txt"}))
	require.NoError(t, err)

	r1, err := tool.Run(ctx, mustJSON(t, map[string]any{"path": "seq.txt", "old_string": "alpha", "new_string": "ALPHA"}))
	require.NoError(t, err)
	require.False(t, r1.IsError, "first edit after view should succeed: %s", r1.Content)

	r2, err := tool.Run(ctx, mustJSON(t, map[string]any{"path": "seq.txt", "old_string": "gamma", "new_string": "GAMMA"}))
	require.NoError(t, err)
	require.False(t, r2.IsError, "second consecutive edit should succeed (baseline refreshed): %s", r2.Content)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "ALPHA beta GAMMA\n", string(got))
}

func TestEditResultIncludesUnifiedDiff(t *testing.T) {
	workDir := t.TempDir()
	path := filepath.Join(workDir, "diff.txt")
	require.NoError(t, os.WriteFile(path, []byte("alpha\nbeta\ngamma\n"), 0o644))

	tool := newEditTool(Dependencies{WorkDir: workDir, SessionID: "edit-diff"})
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"path":       "diff.txt",
		"old_string": "beta",
		"new_string": "BETA",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Contains(t, result.Content, "1 replacement(s)")
	// The model should see exactly which line changed, with context.
	require.Contains(t, result.Content, "@@")
	require.Contains(t, result.Content, "-beta")
	require.Contains(t, result.Content, "+BETA")
	require.Contains(t, result.Content, " alpha")
	require.NotEmpty(t, result.Metadata["diff"])
}
