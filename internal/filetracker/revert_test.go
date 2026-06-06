package filetracker

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// snapTracker builds a Tracker backed by a fresh DB whose snapshot blob
// store lives under a temp dir, plus a working dir for files.
func snapTracker(t *testing.T) (*Tracker, string) {
	t.Helper()
	database := setupTestDB(t)
	blobDir := filepath.Join(t.TempDir(), "snapshots")
	tr := NewTrackerWithSnapshots(database, nil, blobDir)
	return tr, t.TempDir()
}

// recordEdit mirrors the edit tool: the new content is on disk, then the
// change (old -> new) is recorded.
func recordEdit(t *testing.T, tr *Tracker, sid, path string, oldContent, newContent []byte) {
	t.Helper()
	if newContent == nil {
		require.NoError(t, os.Remove(path))
	} else {
		require.NoError(t, os.WriteFile(path, newContent, 0o644))
	}
	_, err := tr.RecordWrite(context.Background(), sid, path, oldContent, newContent)
	require.NoError(t, err)
}

func TestRevertSession_RestoresEditedFile(t *testing.T) {
	tr, dir := snapTracker(t)
	sid := "s1"
	createTestSession(t, tr.database, sid)
	path := filepath.Join(dir, "a.txt")

	require.NoError(t, os.WriteFile(path, []byte("original\n"), 0o644))
	recordEdit(t, tr, sid, path, []byte("original\n"), []byte("changed\n"))

	out, err := tr.RevertSession(context.Background(), sid, RevertOptions{})
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Equal(t, RevertRestored, out[0].Action)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "original\n", string(got))
}

func TestRevertSession_MultipleEditsRestoreToEarliestOriginal(t *testing.T) {
	tr, dir := snapTracker(t)
	sid := "s1"
	createTestSession(t, tr.database, sid)
	path := filepath.Join(dir, "a.txt")

	require.NoError(t, os.WriteFile(path, []byte("v0\n"), 0o644))
	recordEdit(t, tr, sid, path, []byte("v0\n"), []byte("v1\n"))
	recordEdit(t, tr, sid, path, []byte("v1\n"), []byte("v2\n"))

	out, err := tr.RevertSession(context.Background(), sid, RevertOptions{})
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Equal(t, RevertRestored, out[0].Action)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "v0\n", string(got))
}

func TestRevertSession_DeletesCreatedFile(t *testing.T) {
	tr, dir := snapTracker(t)
	sid := "s1"
	createTestSession(t, tr.database, sid)
	path := filepath.Join(dir, "new.txt")

	recordEdit(t, tr, sid, path, nil, []byte("brand new\n")) // create

	out, err := tr.RevertSession(context.Background(), sid, RevertOptions{})
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Equal(t, RevertDeleted, out[0].Action)
	require.NoFileExists(t, path)
}

func TestRevertSession_RestoresDeletedFile(t *testing.T) {
	tr, dir := snapTracker(t)
	sid := "s1"
	createTestSession(t, tr.database, sid)
	path := filepath.Join(dir, "gone.txt")

	require.NoError(t, os.WriteFile(path, []byte("keep me\n"), 0o644))
	recordEdit(t, tr, sid, path, []byte("keep me\n"), nil) // delete

	out, err := tr.RevertSession(context.Background(), sid, RevertOptions{})
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Equal(t, RevertRestored, out[0].Action)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "keep me\n", string(got))
}

func TestRevertSession_SkipsModifiedFileUnlessForced(t *testing.T) {
	tr, dir := snapTracker(t)
	sid := "s1"
	createTestSession(t, tr.database, sid)
	path := filepath.Join(dir, "a.txt")

	require.NoError(t, os.WriteFile(path, []byte("original\n"), 0o644))
	recordEdit(t, tr, sid, path, []byte("original\n"), []byte("agent edit\n"))

	// Out-of-band modification after the agent wrote it.
	require.NoError(t, os.WriteFile(path, []byte("user edit\n"), 0o644))

	out, err := tr.RevertSession(context.Background(), sid, RevertOptions{})
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Equal(t, RevertSkipped, out[0].Action)
	require.NotEmpty(t, out[0].Reason)
	got, _ := os.ReadFile(path)
	require.Equal(t, "user edit\n", string(got), "skipped file must be untouched")

	// With Force the original is restored regardless.
	out, err = tr.RevertSession(context.Background(), sid, RevertOptions{Force: true})
	require.NoError(t, err)
	require.Equal(t, RevertRestored, out[0].Action)
	got, _ = os.ReadFile(path)
	require.Equal(t, "original\n", string(got))
}

func TestRevertSession_DryRunReportsButDoesNotChange(t *testing.T) {
	tr, dir := snapTracker(t)
	sid := "s1"
	createTestSession(t, tr.database, sid)
	path := filepath.Join(dir, "a.txt")

	require.NoError(t, os.WriteFile(path, []byte("original\n"), 0o644))
	recordEdit(t, tr, sid, path, []byte("original\n"), []byte("changed\n"))

	out, err := tr.RevertSession(context.Background(), sid, RevertOptions{DryRun: true})
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Equal(t, RevertRestored, out[0].Action)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "changed\n", string(got), "dry run must not modify the file")
}

func TestRevertSession_NoSnapshotSkips(t *testing.T) {
	database := setupTestDB(t)
	tr := NewTracker(database, nil) // snapshots disabled
	dir := t.TempDir()
	sid := "s1"
	createTestSession(t, database, sid)
	path := filepath.Join(dir, "a.txt")

	require.NoError(t, os.WriteFile(path, []byte("original\n"), 0o644))
	recordEdit(t, tr, sid, path, []byte("original\n"), []byte("changed\n"))

	out, err := tr.RevertSession(context.Background(), sid, RevertOptions{})
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Equal(t, RevertSkipped, out[0].Action)
	require.Contains(t, out[0].Reason, "snapshot")
}

func TestRevertSession_EmptySessionNoOutcomes(t *testing.T) {
	tr, _ := snapTracker(t)
	sid := "s1"
	createTestSession(t, tr.database, sid)

	out, err := tr.RevertSession(context.Background(), sid, RevertOptions{})
	require.NoError(t, err)
	require.Empty(t, out)
}
