package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPatchAppliesMultiFileChangeSet exercises the headline capability: a single
// unified diff that creates, modifies, and deletes files in one atomic call.
func TestPatchAppliesMultiFileChangeSet(t *testing.T) {
	ctx := context.Background()
	workDir := t.TempDir()
	mod := filepath.Join(workDir, "mod.txt")
	del := filepath.Join(workDir, "del.txt")
	require.NoError(t, os.WriteFile(mod, []byte("line1\nline2\nline3\n"), 0o644))
	require.NoError(t, os.WriteFile(del, []byte("gone\n"), 0o644))

	tracker := newToolsTestTracker(t, "patch-multi")
	require.NoError(t, tracker.RecordRead(ctx, "patch-multi", mod))
	require.NoError(t, tracker.RecordRead(ctx, "patch-multi", del))
	tool := newPatchTool(Dependencies{FileTracker: tracker, WorkDir: workDir, SessionID: "patch-multi"})

	patch := "--- a/mod.txt\n" +
		"+++ b/mod.txt\n" +
		"@@ -1,3 +1,3 @@\n" +
		" line1\n" +
		"-line2\n" +
		"+LINE2\n" +
		" line3\n" +
		"--- /dev/null\n" +
		"+++ b/sub/new.txt\n" +
		"@@ -0,0 +1,2 @@\n" +
		"+hello\n" +
		"+world\n" +
		"--- a/del.txt\n" +
		"+++ /dev/null\n" +
		"@@ -1 +0,0 @@\n" +
		"-gone\n"

	result, err := tool.Run(ctx, mustJSON(t, map[string]any{"patch": patch}))
	require.NoError(t, err)
	require.False(t, result.IsError, result.Content)

	gotMod, err := os.ReadFile(mod)
	require.NoError(t, err)
	require.Equal(t, "line1\nLINE2\nline3\n", string(gotMod))

	gotNew, err := os.ReadFile(filepath.Join(workDir, "sub", "new.txt"))
	require.NoError(t, err)
	require.Equal(t, "hello\nworld\n", string(gotNew))

	_, err = os.Stat(del)
	require.True(t, os.IsNotExist(err), "deleted file should be gone")

	require.Contains(t, result.Content, "applied patch to 3 files")
	require.Contains(t, result.Content, "-line2")
	require.Contains(t, result.Content, "+LINE2")
	require.Equal(t, 3, result.Metadata["count"])
}

// TestPatchIsAtomicOnHunkMismatch verifies that a later file failing to apply
// leaves the earlier, valid file untouched: validation precedes any write.
func TestPatchIsAtomicOnHunkMismatch(t *testing.T) {
	ctx := context.Background()
	workDir := t.TempDir()
	good := filepath.Join(workDir, "good.txt")
	bad := filepath.Join(workDir, "bad.txt")
	require.NoError(t, os.WriteFile(good, []byte("alpha\nbeta\n"), 0o644))
	require.NoError(t, os.WriteFile(bad, []byte("actual content\n"), 0o644))

	tracker := newToolsTestTracker(t, "patch-atomic")
	require.NoError(t, tracker.RecordRead(ctx, "patch-atomic", good))
	require.NoError(t, tracker.RecordRead(ctx, "patch-atomic", bad))
	tool := newPatchTool(Dependencies{FileTracker: tracker, WorkDir: workDir, SessionID: "patch-atomic"})

	patch := "--- a/good.txt\n" +
		"+++ b/good.txt\n" +
		"@@ -1,2 +1,2 @@\n" +
		"-alpha\n" +
		"+ALPHA\n" +
		" beta\n" +
		"--- a/bad.txt\n" +
		"+++ b/bad.txt\n" +
		"@@ -1,1 +1,1 @@\n" +
		"-context that is not in the file\n" +
		"+replacement\n"

	result, err := tool.Run(ctx, mustJSON(t, map[string]any{"patch": patch}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content, "bad.txt")

	gotGood, err := os.ReadFile(good)
	require.NoError(t, err)
	require.Equal(t, "alpha\nbeta\n", string(gotGood), "valid file must be untouched when patch fails")
}

// TestPatchRejectsUnreadFile confirms the read-before-edit guard fires for a
// modified file the session never viewed.
func TestPatchRejectsUnreadFile(t *testing.T) {
	ctx := context.Background()
	workDir := t.TempDir()
	path := filepath.Join(workDir, "unseen.txt")
	require.NoError(t, os.WriteFile(path, []byte("one\n"), 0o644))

	tracker := newToolsTestTracker(t, "patch-unread")
	tool := newPatchTool(Dependencies{FileTracker: tracker, WorkDir: workDir, SessionID: "patch-unread"})

	patch := "--- a/unseen.txt\n" +
		"+++ b/unseen.txt\n" +
		"@@ -1 +1 @@\n" +
		"-one\n" +
		"+two\n"

	result, err := tool.Run(ctx, mustJSON(t, map[string]any{"patch": patch}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content, "has not been read")

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "one\n", string(got))
}

// TestPatchModifyMissingFile reports a clear error when a modify section targets
// a file that does not exist.
func TestPatchModifyMissingFile(t *testing.T) {
	workDir := t.TempDir()
	tool := newPatchTool(Dependencies{WorkDir: workDir, SessionID: "patch-missing"})
	patch := "--- a/nope.txt\n" +
		"+++ b/nope.txt\n" +
		"@@ -1 +1 @@\n" +
		"-a\n" +
		"+b\n"
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{"patch": patch}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content, "does not exist")
}

// TestPatchRejectsOutsideWorkspace blocks a path that escapes the workspace.
func TestPatchRejectsOutsideWorkspace(t *testing.T) {
	workDir := t.TempDir()
	tool := newPatchTool(Dependencies{WorkDir: workDir, SessionID: "patch-escape"})
	patch := "--- a/../../etc/passwd\n" +
		"+++ b/../../etc/passwd\n" +
		"@@ -1 +1 @@\n" +
		"-a\n" +
		"+b\n"
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{"patch": patch}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content, "outside the workspace")
}

// TestPatchCreateExistingFails refuses to clobber an existing file via a
// /dev/null create section.
func TestPatchCreateExistingFails(t *testing.T) {
	workDir := t.TempDir()
	path := filepath.Join(workDir, "here.txt")
	require.NoError(t, os.WriteFile(path, []byte("present\n"), 0o644))
	tool := newPatchTool(Dependencies{WorkDir: workDir, SessionID: "patch-clobber"})
	patch := "--- /dev/null\n" +
		"+++ b/here.txt\n" +
		"@@ -0,0 +1 @@\n" +
		"+new\n"
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{"patch": patch}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content, "already exists")
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "present\n", string(got))
}

// TestPatchToleratesGitPreambleAndDrift parses git-style preamble lines and
// applies a hunk whose declared line number has drifted, via the content-search
// fallback.
func TestPatchToleratesGitPreambleAndDrift(t *testing.T) {
	ctx := context.Background()
	workDir := t.TempDir()
	path := filepath.Join(workDir, "drift.txt")
	// The hunk header below claims the block starts at line 1, but it actually
	// starts at line 3 — the forward search must still find it.
	require.NoError(t, os.WriteFile(path, []byte("x\ny\ntarget\nz\n"), 0o644))

	tracker := newToolsTestTracker(t, "patch-drift")
	require.NoError(t, tracker.RecordRead(ctx, "patch-drift", path))
	tool := newPatchTool(Dependencies{FileTracker: tracker, WorkDir: workDir, SessionID: "patch-drift"})

	patch := "diff --git a/drift.txt b/drift.txt\n" +
		"index 1111111..2222222 100644\n" +
		"--- a/drift.txt\t2026-01-01 00:00:00\n" +
		"+++ b/drift.txt\t2026-01-01 00:00:01\n" +
		"@@ -1 +1 @@\n" +
		"-target\n" +
		"+TARGET\n"

	result, err := tool.Run(ctx, mustJSON(t, map[string]any{"patch": patch}))
	require.NoError(t, err)
	require.False(t, result.IsError, result.Content)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "x\ny\nTARGET\nz\n", string(got))
}

// TestPatchInsertionHunkWithContext applies a normal git insertion (context
// lines surrounding added lines) without disturbing the surrounding text.
func TestPatchInsertionHunkWithContext(t *testing.T) {
	ctx := context.Background()
	workDir := t.TempDir()
	path := filepath.Join(workDir, "ins.txt")
	require.NoError(t, os.WriteFile(path, []byte("a\nb\nc\n"), 0o644))

	tracker := newToolsTestTracker(t, "patch-ins")
	require.NoError(t, tracker.RecordRead(ctx, "patch-ins", path))
	tool := newPatchTool(Dependencies{FileTracker: tracker, WorkDir: workDir, SessionID: "patch-ins"})

	patch := "--- a/ins.txt\n" +
		"+++ b/ins.txt\n" +
		"@@ -1,3 +1,4 @@\n" +
		" a\n" +
		" b\n" +
		"+inserted\n" +
		" c\n"

	result, err := tool.Run(ctx, mustJSON(t, map[string]any{"patch": patch}))
	require.NoError(t, err)
	require.False(t, result.IsError, result.Content)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "a\nb\ninserted\nc\n", string(got))
}

func TestPatchInvalidArguments(t *testing.T) {
	tool := newPatchTool(Dependencies{WorkDir: t.TempDir()})

	empty, err := tool.Run(context.Background(), mustJSON(t, map[string]any{"patch": "   "}))
	require.NoError(t, err)
	require.True(t, empty.IsError)
	require.Contains(t, empty.Content, "patch is required")

	garbage, err := tool.Run(context.Background(), mustJSON(t, map[string]any{"patch": "not a diff at all\n"}))
	require.NoError(t, err)
	require.True(t, garbage.IsError)
	require.Contains(t, garbage.Content, "no file sections found")
}

// TestParseUnifiedPatchDefaultsAndCounts checks header parsing edge cases
// directly: single-line ranges default the count to 1, and a body that does not
// match the declared counts is rejected.
func TestParseUnifiedPatchDefaultsAndCounts(t *testing.T) {
	files, err := parseUnifiedPatch("--- a/f\n+++ b/f\n@@ -2 +2 @@\n-old\n+new\n")
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Len(t, files[0].hunks, 1)
	require.Equal(t, 2, files[0].hunks[0].oldStart)
	require.Equal(t, []string{"old"}, files[0].hunks[0].oldLines)
	require.Equal(t, []string{"new"}, files[0].hunks[0].newLines)

	_, err = parseUnifiedPatch("--- a/f\n+++ b/f\n@@ -1,5 +1,5 @@\n-old\n+new\n")
	require.Error(t, err, "body shorter than declared counts must error")
	require.Contains(t, err.Error(), "declared")
}

// TestApplyFilePatchPreservesNoTrailingNewline confirms a file without a final
// newline keeps that shape after patching.
func TestApplyFilePatchPreservesNoTrailingNewline(t *testing.T) {
	fp := filePatch{hunks: []patchHunk{{oldStart: 1, oldLines: []string{"a", "b"}, newLines: []string{"a", "B"}}}}
	out, err := applyFilePatch(fp, "a\nb")
	require.NoError(t, err)
	require.Equal(t, "a\nB", out)

	fp2 := filePatch{hunks: []patchHunk{{oldStart: 1, oldLines: []string{"a", "b"}, newLines: []string{"a", "B"}}}}
	out2, err := applyFilePatch(fp2, "a\nb\n")
	require.NoError(t, err)
	require.Equal(t, "a\nB\n", out2)
}
