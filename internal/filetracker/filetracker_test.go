package filetracker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/db"
	"github.com/arbazkhan971/bharatcode/internal/db/sqlc"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/stretchr/testify/require"
)

func setupTestDB(t *testing.T) *db.DB {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	database, err := db.Open(context.Background(), dbPath)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = database.Close()
	})
	return database
}

func createTestSession(t *testing.T, database *db.DB, id string) {
	_, err := database.Queries.CreateSession(context.Background(), sqlc.CreateSessionParams{
		ID:          id,
		ProjectPath: "/some/project",
		Title:       "Test Session",
		Model:       "gpt-4",
		Agent:       "code-agent",
		CreatedAt:   time.Now().UnixMilli(),
		UpdatedAt:   time.Now().UnixMilli(),
	})
	require.NoError(t, err)
}

func TestNewTracker_NilBus_Allowed(t *testing.T) {
	database := setupTestDB(t)
	tracker := NewTracker(database, nil)
	require.NotNil(t, tracker)
}

func TestRecordRead_StoresHash(t *testing.T) {
	database := setupTestDB(t)
	sid := "session-1"
	createTestSession(t, database, sid)
	tracker := NewTracker(database, nil)

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.txt")
	err := os.WriteFile(filePath, []byte("hello"), 0o644)
	require.NoError(t, err)

	ctx := context.Background()
	err = tracker.RecordRead(ctx, sid, filePath)
	require.NoError(t, err)

	conflict, err := tracker.HasConflict(ctx, sid, filePath)
	require.NoError(t, err)
	require.False(t, conflict)
}

func TestRecordRead_MissingFile_StoresEmptyHash(t *testing.T) {
	database := setupTestDB(t)
	sid := "session-1"
	createTestSession(t, database, sid)
	tracker := NewTracker(database, nil)

	filePath := filepath.Join(t.TempDir(), "nonexistent.txt")
	ctx := context.Background()
	err := tracker.RecordRead(ctx, sid, filePath)
	require.NoError(t, err)

	conflict, err := tracker.HasConflict(ctx, sid, filePath)
	require.NoError(t, err)
	require.False(t, conflict)
}

func TestRecordRead_Idempotent(t *testing.T) {
	database := setupTestDB(t)
	sid := "session-1"
	createTestSession(t, database, sid)
	tracker := NewTracker(database, nil)

	filePath := filepath.Join(t.TempDir(), "test.txt")
	err := os.WriteFile(filePath, []byte("hello"), 0o644)
	require.NoError(t, err)

	ctx := context.Background()
	err = tracker.RecordRead(ctx, sid, filePath)
	require.NoError(t, err)

	err = tracker.RecordRead(ctx, sid, filePath)
	require.NoError(t, err)

	// Verify only one entry exists.
	read, err := database.Queries.GetFileRead(ctx, sqlc.GetFileReadParams{
		SessionID: sid,
		Path:      filePath,
	})
	require.NoError(t, err)
	h := sha256.Sum256([]byte("hello"))
	require.Equal(t, hex.EncodeToString(h[:]), read.Hash)
}

func TestRecordWrite_InfersCreate(t *testing.T) {
	database := setupTestDB(t)
	sid := "session-1"
	createTestSession(t, database, sid)
	tracker := NewTracker(database, nil)

	filePath := filepath.Join(t.TempDir(), "create.txt")
	ctx := context.Background()
	change, err := tracker.RecordWrite(ctx, sid, filePath, nil, []byte("new content"))
	require.NoError(t, err)

	require.Equal(t, OpCreate, change.Op)
	require.Equal(t, "", change.BeforeHash)
	h := sha256.Sum256([]byte("new content"))
	require.Equal(t, hex.EncodeToString(h[:]), change.AfterHash)
}

func TestRecordWrite_InfersEdit(t *testing.T) {
	database := setupTestDB(t)
	sid := "session-1"
	createTestSession(t, database, sid)
	tracker := NewTracker(database, nil)

	filePath := filepath.Join(t.TempDir(), "edit.txt")
	ctx := context.Background()
	change, err := tracker.RecordWrite(ctx, sid, filePath, []byte("old content"), []byte("new content"))
	require.NoError(t, err)

	require.Equal(t, OpEdit, change.Op)
	hOld := sha256.Sum256([]byte("old content"))
	require.Equal(t, hex.EncodeToString(hOld[:]), change.BeforeHash)
	hNew := sha256.Sum256([]byte("new content"))
	require.Equal(t, hex.EncodeToString(hNew[:]), change.AfterHash)
}

func TestRecordWrite_InfersDelete(t *testing.T) {
	database := setupTestDB(t)
	sid := "session-1"
	createTestSession(t, database, sid)
	tracker := NewTracker(database, nil)

	filePath := filepath.Join(t.TempDir(), "delete.txt")
	ctx := context.Background()
	change, err := tracker.RecordWrite(ctx, sid, filePath, []byte("old content"), nil)
	require.NoError(t, err)

	require.Equal(t, OpDelete, change.Op)
	hOld := sha256.Sum256([]byte("old content"))
	require.Equal(t, hex.EncodeToString(hOld[:]), change.BeforeHash)
	require.Equal(t, "", change.AfterHash)
}

func TestRecordWrite_PublishesOnBus(t *testing.T) {
	database := setupTestDB(t)
	sid := "session-1"
	createTestSession(t, database, sid)
	bus := pubsub.NewTopic[Change]("file_changes", 10)
	tracker := NewTracker(database, bus)

	ch, cancel := bus.Subscribe()
	defer cancel()

	filePath := filepath.Join(t.TempDir(), "pub.txt")
	ctx := context.Background()
	change, err := tracker.RecordWrite(ctx, sid, filePath, nil, []byte("hello"))
	require.NoError(t, err)

	select {
	case received := <-ch:
		require.Equal(t, change, received)
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for change event on bus")
	}
}

func TestRecordWrite_NilBus_NoPanic(t *testing.T) {
	database := setupTestDB(t)
	sid := "session-1"
	createTestSession(t, database, sid)
	tracker := NewTracker(database, nil)

	filePath := filepath.Join(t.TempDir(), "nilbus.txt")
	ctx := context.Background()
	_, err := tracker.RecordWrite(ctx, sid, filePath, nil, []byte("no bus"))
	require.NoError(t, err)

	changes, err := tracker.ChangesForSession(ctx, sid)
	require.NoError(t, err)
	require.Len(t, changes, 1)
	h := sha256.Sum256([]byte("no bus"))
	require.Equal(t, hex.EncodeToString(h[:]), changes[0].AfterHash)
}

func TestChangesForSession_OldestFirst(t *testing.T) {
	database := setupTestDB(t)
	sid := "session-1"
	createTestSession(t, database, sid)
	tracker := NewTracker(database, nil)

	ctx := context.Background()
	p1 := filepath.Join(t.TempDir(), "1.txt")
	c1, err := tracker.RecordWrite(ctx, sid, p1, nil, []byte("one"))
	require.NoError(t, err)

	time.Sleep(2 * time.Millisecond)
	p2 := filepath.Join(t.TempDir(), "2.txt")
	c2, err := tracker.RecordWrite(ctx, sid, p2, nil, []byte("two"))
	require.NoError(t, err)

	time.Sleep(2 * time.Millisecond)
	p3 := filepath.Join(t.TempDir(), "3.txt")
	c3, err := tracker.RecordWrite(ctx, sid, p3, nil, []byte("three"))
	require.NoError(t, err)

	changes, err := tracker.ChangesForSession(ctx, sid)
	require.NoError(t, err)
	require.Len(t, changes, 3)
	require.Equal(t, c1.Path, changes[0].Path)
	require.Equal(t, c2.Path, changes[1].Path)
	require.Equal(t, c3.Path, changes[2].Path)
}

func TestChangesForSession_OnlyOwnSession(t *testing.T) {
	database := setupTestDB(t)
	sidA := "session-A"
	sidB := "session-B"
	createTestSession(t, database, sidA)
	createTestSession(t, database, sidB)
	tracker := NewTracker(database, nil)

	ctx := context.Background()
	pA := filepath.Join(t.TempDir(), "A.txt")
	_, err := tracker.RecordWrite(ctx, sidA, pA, nil, []byte("A"))
	require.NoError(t, err)

	pB := filepath.Join(t.TempDir(), "B.txt")
	_, err = tracker.RecordWrite(ctx, sidB, pB, nil, []byte("B"))
	require.NoError(t, err)

	changesA, err := tracker.ChangesForSession(ctx, sidA)
	require.NoError(t, err)
	require.Len(t, changesA, 1)
	require.Equal(t, pA, changesA[0].Path)

	changesB, err := tracker.ChangesForSession(ctx, sidB)
	require.NoError(t, err)
	require.Len(t, changesB, 1)
	require.Equal(t, pB, changesB[0].Path)
}

func TestHasConflict_ExternalEdit_BetweenReadAndWrite(t *testing.T) {
	database := setupTestDB(t)
	sid := "session-1"
	createTestSession(t, database, sid)
	tracker := NewTracker(database, nil)

	filePath := filepath.Join(t.TempDir(), "conflict.txt")
	err := os.WriteFile(filePath, []byte("initial"), 0o644)
	require.NoError(t, err)

	ctx := context.Background()
	err = tracker.RecordRead(ctx, sid, filePath)
	require.NoError(t, err)

	// Mutate file out-of-band.
	err = os.WriteFile(filePath, []byte("external change"), 0o644)
	require.NoError(t, err)

	conflict, err := tracker.HasConflict(ctx, sid, filePath)
	require.NoError(t, err)
	require.True(t, conflict)
}

func TestHasConflict_StaleRead_ReadModifyWrite(t *testing.T) {
	database := setupTestDB(t)
	sid := "session-1"
	createTestSession(t, database, sid)
	tracker := NewTracker(database, nil)

	filePath := filepath.Join(t.TempDir(), "stale.txt")
	original := []byte("v1 original")
	err := os.WriteFile(filePath, original, 0o644)
	require.NoError(t, err)

	ctx := context.Background()

	// Agent reads the file, capturing the baseline hash.
	err = tracker.RecordRead(ctx, sid, filePath)
	require.NoError(t, err)

	// While the agent holds its in-memory view, an external process
	// modifies the file on disk (e.g. another editor or a teammate).
	err = os.WriteFile(filePath, []byte("v2 modified externally"), 0o644)
	require.NoError(t, err)

	// The agent now tries to persist an edit derived from the stale
	// content it read. RecordWrite does not touch disk; it records the
	// agent's intended mutation.
	_, err = tracker.RecordWrite(ctx, sid, filePath, original, []byte("v2 agent edit"))
	require.NoError(t, err)

	// The on-disk content no longer matches the hash captured at read
	// time, so the write is based on a stale read and must be flagged.
	conflict, err := tracker.HasConflict(ctx, sid, filePath)
	require.NoError(t, err)
	require.True(t, conflict)
}

func TestChangedFiles_DedupAndSorted(t *testing.T) {
	database := setupTestDB(t)
	sid := "session-1"
	createTestSession(t, database, sid)
	tracker := NewTracker(database, nil)

	ctx := context.Background()
	tmp := t.TempDir()
	pZebra := filepath.Join(tmp, "zebra.txt")
	pApple := filepath.Join(tmp, "apple.txt")

	// Sequence: create zebra, edit zebra twice, create apple. zebra is
	// written three times but must appear once; output sorted by path.
	_, err := tracker.RecordWrite(ctx, sid, pZebra, nil, []byte("z1"))
	require.NoError(t, err)
	_, err = tracker.RecordWrite(ctx, sid, pZebra, []byte("z1"), []byte("z2"))
	require.NoError(t, err)
	_, err = tracker.RecordWrite(ctx, sid, pZebra, []byte("z2"), []byte("z3"))
	require.NoError(t, err)
	_, err = tracker.RecordWrite(ctx, sid, pApple, nil, []byte("a1"))
	require.NoError(t, err)

	files, err := tracker.ChangedFiles(ctx, sid)
	require.NoError(t, err)
	require.Equal(t, []string{pApple, pZebra}, files)
}

func TestChangedFiles_DeleteOnlyExcluded_CreatedThenDeletedIncluded(t *testing.T) {
	database := setupTestDB(t)
	sid := "session-1"
	createTestSession(t, database, sid)
	tracker := NewTracker(database, nil)

	ctx := context.Background()
	tmp := t.TempDir()
	pCreated := filepath.Join(tmp, "created.txt")
	pDeletedOnly := filepath.Join(tmp, "deleted_only.txt")

	// created.txt is created then deleted: still a changed file because
	// it was created during the session.
	_, err := tracker.RecordWrite(ctx, sid, pCreated, nil, []byte("hello"))
	require.NoError(t, err)
	_, err = tracker.RecordWrite(ctx, sid, pCreated, []byte("hello"), nil)
	require.NoError(t, err)

	// deleted_only.txt is only ever deleted (e.g. a pre-existing file the
	// agent removed); it is not a created/edited file, so it is excluded.
	_, err = tracker.RecordWrite(ctx, sid, pDeletedOnly, []byte("preexisting"), nil)
	require.NoError(t, err)

	files, err := tracker.ChangedFiles(ctx, sid)
	require.NoError(t, err)
	require.Equal(t, []string{pCreated}, files)
}

func TestChangedFiles_EmptySession_ReturnsEmptyNonNil(t *testing.T) {
	database := setupTestDB(t)
	sid := "session-empty"
	createTestSession(t, database, sid)
	tracker := NewTracker(database, nil)

	files, err := tracker.ChangedFiles(context.Background(), sid)
	require.NoError(t, err)
	require.NotNil(t, files)
	require.Empty(t, files)
}

func TestChangedFiles_OnlyOwnSession(t *testing.T) {
	database := setupTestDB(t)
	sidA := "session-A"
	sidB := "session-B"
	createTestSession(t, database, sidA)
	createTestSession(t, database, sidB)
	tracker := NewTracker(database, nil)

	ctx := context.Background()
	pA := filepath.Join(t.TempDir(), "a.txt")
	pB := filepath.Join(t.TempDir(), "b.txt")
	_, err := tracker.RecordWrite(ctx, sidA, pA, nil, []byte("a"))
	require.NoError(t, err)
	_, err = tracker.RecordWrite(ctx, sidB, pB, nil, []byte("b"))
	require.NoError(t, err)

	filesA, err := tracker.ChangedFiles(ctx, sidA)
	require.NoError(t, err)
	require.Equal(t, []string{pA}, filesA)
}

func TestChangedFiles_ContextCanceled_Errors(t *testing.T) {
	database := setupTestDB(t)
	sid := "session-1"
	createTestSession(t, database, sid)
	tracker := NewTracker(database, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := tracker.ChangedFiles(ctx, sid)
	require.Error(t, err)
}

func TestHasConflict_NoReadRecorded_ReturnsFalse(t *testing.T) {
	database := setupTestDB(t)
	sid := "session-1"
	createTestSession(t, database, sid)
	tracker := NewTracker(database, nil)

	filePath := filepath.Join(t.TempDir(), "noread.txt")
	err := os.WriteFile(filePath, []byte("hello"), 0o644)
	require.NoError(t, err)

	ctx := context.Background()
	conflict, err := tracker.HasConflict(ctx, sid, filePath)
	require.NoError(t, err)
	require.False(t, conflict)
}

func TestHasConflict_FileDeletedExternally(t *testing.T) {
	database := setupTestDB(t)
	sid := "session-1"
	createTestSession(t, database, sid)
	tracker := NewTracker(database, nil)

	filePath := filepath.Join(t.TempDir(), "delete_extern.txt")
	err := os.WriteFile(filePath, []byte("initial"), 0o644)
	require.NoError(t, err)

	ctx := context.Background()
	err = tracker.RecordRead(ctx, sid, filePath)
	require.NoError(t, err)

	err = os.Remove(filePath)
	require.NoError(t, err)

	conflict, err := tracker.HasConflict(ctx, sid, filePath)
	require.NoError(t, err)
	require.True(t, conflict)
}

func TestHasConflict_UnchangedFile(t *testing.T) {
	database := setupTestDB(t)
	sid := "session-1"
	createTestSession(t, database, sid)
	tracker := NewTracker(database, nil)

	filePath := filepath.Join(t.TempDir(), "unchanged.txt")
	err := os.WriteFile(filePath, []byte("hello"), 0o644)
	require.NoError(t, err)

	ctx := context.Background()
	err = tracker.RecordRead(ctx, sid, filePath)
	require.NoError(t, err)

	conflict, err := tracker.HasConflict(ctx, sid, filePath)
	require.NoError(t, err)
	require.False(t, conflict)
}

func TestConcurrentRecordWrite_NoDataRace(t *testing.T) {
	database := setupTestDB(t)
	sid := "session-1"
	createTestSession(t, database, sid)
	tracker := NewTracker(database, nil)

	ctx := context.Background()
	numGoroutines := 16
	var wg sync.WaitGroup

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			filePath := filepath.Join(t.TempDir(), fmt.Sprintf("file_%d.txt", workerID))

			// Perform a read
			err := os.WriteFile(filePath, []byte(fmt.Sprintf("content_%d", workerID)), 0o644)
			require.NoError(t, err)

			err = tracker.RecordRead(ctx, sid, filePath)
			require.NoError(t, err)

			// Verify no conflict
			conflict, err := tracker.HasConflict(ctx, sid, filePath)
			require.NoError(t, err)
			require.False(t, conflict)

			// Perform a write
			_, err = tracker.RecordWrite(ctx, sid, filePath, []byte(fmt.Sprintf("content_%d", workerID)), []byte(fmt.Sprintf("new_content_%d", workerID)))
			require.NoError(t, err)
		}(i)
	}

	wg.Wait()

	changes, err := tracker.ChangesForSession(ctx, sid)
	require.NoError(t, err)
	require.Len(t, changes, numGoroutines)
}

func TestRecordRead_DirError(t *testing.T) {
	database := setupTestDB(t)
	sid := "session-1"
	createTestSession(t, database, sid)
	tracker := NewTracker(database, nil)

	dirPath := t.TempDir()
	ctx := context.Background()
	err := tracker.RecordRead(ctx, sid, dirPath)
	require.Error(t, err)
}

func TestRecordWrite_DirError(t *testing.T) {
	database := setupTestDB(t)
	sid := "session-1"
	createTestSession(t, database, sid)
	tracker := NewTracker(database, nil)

	dirPath := t.TempDir()
	ctx := context.Background()
	_, err := tracker.RecordWrite(ctx, sid, dirPath, nil, []byte("content"))
	require.Error(t, err)
}

func TestHasConflict_DirError(t *testing.T) {
	database := setupTestDB(t)
	sid := "session-1"
	createTestSession(t, database, sid)
	tracker := NewTracker(database, nil)

	dirPath := t.TempDir()
	ctx := context.Background()
	_, err := tracker.HasConflict(ctx, sid, dirPath)
	require.Error(t, err)
}

func TestRecordWrite_BothNilError(t *testing.T) {
	database := setupTestDB(t)
	sid := "session-1"
	createTestSession(t, database, sid)
	tracker := NewTracker(database, nil)

	filePath := filepath.Join(t.TempDir(), "both_nil.txt")
	ctx := context.Background()
	_, err := tracker.RecordWrite(ctx, sid, filePath, nil, nil)
	require.Error(t, err)
}

type errorReader struct{}

func (errorReader) Read(b []byte) (int, error) {
	return 0, fmt.Errorf("injected rand error")
}

func TestRecordWrite_UUIDError(t *testing.T) {
	database := setupTestDB(t)
	sid := "session-1"
	createTestSession(t, database, sid)
	tracker := NewTracker(database, nil)

	restoreReader := setUUIDRandomReader(errorReader{})
	t.Cleanup(restoreReader)

	filePath := filepath.Join(t.TempDir(), "uuid_err.txt")
	ctx := context.Background()
	_, err := tracker.RecordWrite(ctx, sid, filePath, nil, []byte("content"))
	require.Error(t, err)
}

func TestCoverage_DBErrors(t *testing.T) {
	database := setupTestDB(t)
	sid := "session-1"
	createTestSession(t, database, sid)
	tracker := NewTracker(database, nil)

	filePath := filepath.Join(t.TempDir(), "db_err.txt")
	err := os.WriteFile(filePath, []byte("hello"), 0o644)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// RecordRead fails due to canceled context.
	err = tracker.RecordRead(ctx, sid, filePath)
	require.Error(t, err)

	// RecordWrite fails due to canceled context.
	_, err = tracker.RecordWrite(ctx, sid, filePath, nil, []byte("content"))
	require.Error(t, err)

	// ChangesForSession fails due to canceled context.
	_, err = tracker.ChangesForSession(ctx, sid)
	require.Error(t, err)

	// HasConflict fails due to canceled context.
	_, err = tracker.HasConflict(ctx, sid, filePath)
	require.Error(t, err)
}

func TestCoverage_PermissionErrors(t *testing.T) {
	database := setupTestDB(t)
	sid := "session-1"
	createTestSession(t, database, sid)
	tracker := NewTracker(database, nil)

	// Since we run as root, we can trigger os.Stat error (ENOTDIR) by traversing a regular file
	// as if it were a directory.
	filePath := filepath.Join(t.TempDir(), "regular.txt")
	err := os.WriteFile(filePath, []byte("test"), 0o644)
	require.NoError(t, err)

	invalidPath := filepath.Join(filePath, "subfile.txt")
	ctx := context.Background()
	err = tracker.RecordRead(ctx, sid, invalidPath)
	require.Error(t, err)

	// We can trigger os.ReadFile error by attempting to read a directory.
	dirPath := t.TempDir()
	err = tracker.RecordRead(ctx, sid, dirPath)
	require.Error(t, err)
}

func TestCoverage_NilCache(t *testing.T) {
	database := setupTestDB(t)
	sid := "session-1"
	createTestSession(t, database, sid)

	// Construct tracker manually with nil cache.
	tracker := &Tracker{
		database: database,
		queries:  database.Queries,
	}

	filePath := filepath.Join(t.TempDir(), "nilcache.txt")
	err := os.WriteFile(filePath, []byte("test"), 0o644)
	require.NoError(t, err)

	ctx := context.Background()
	err = tracker.RecordRead(ctx, sid, filePath)
	require.NoError(t, err)
	require.NotNil(t, tracker.lastReadCache)

	// Reset cache to test HasConflict path.
	tracker.lastReadCache = nil
	conflict, err := tracker.HasConflict(ctx, sid, filePath)
	require.NoError(t, err)
	require.False(t, conflict)
	require.NotNil(t, tracker.lastReadCache)
}

func TestCoverage_CustomAndDeletedChanges(t *testing.T) {
	database := setupTestDB(t)
	sid := "session-1"
	createTestSession(t, database, sid)
	tracker := NewTracker(database, nil)

	ctx := context.Background()

	// Insert custom operation "rename".
	_, err := database.Queries.RecordFileChange(ctx, sqlc.RecordFileChangeParams{
		ID:        "change-rename",
		SessionID: sid,
		Path:      "/some/path",
		Operation: "rename",
		CreatedAt: time.Now().UnixMilli(),
	})
	require.NoError(t, err)

	// Record a delete change where AfterHash will be nil.
	filePath := filepath.Join(t.TempDir(), "deleted_file.txt")
	err = os.WriteFile(filePath, []byte("initial"), 0o644)
	require.NoError(t, err)

	_, err = tracker.RecordWrite(ctx, sid, filePath, []byte("initial"), nil)
	require.NoError(t, err)

	changes, err := tracker.ChangesForSession(ctx, sid)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(changes), 2)

	var foundRename bool
	var foundDelete bool
	for _, c := range changes {
		if c.Op == "rename" {
			foundRename = true
		}
		if c.Op == OpDelete {
			foundDelete = true
			require.Equal(t, "ac1b5c0961a7269b6a053ee64276ed0e20a7f48aefb9f67519539d23aaf10149", c.BeforeHash)
			require.Equal(t, "", c.AfterHash)
		}
	}
	require.True(t, foundRename)
	require.True(t, foundDelete)

	// Test HasConflict where path does not exist but non-empty read hash was recorded (cache missing).
	tracker.lastReadCache = nil
	filePath2 := filepath.Join(t.TempDir(), "missing_but_read.txt")
	err = os.WriteFile(filePath2, []byte("hello"), 0o644)
	require.NoError(t, err)

	err = tracker.RecordRead(ctx, sid, filePath2)
	require.NoError(t, err)

	err = os.Remove(filePath2)
	require.NoError(t, err)

	// Reset cache to force DB lookup in HasConflict.
	tracker.lastReadCache = nil
	conflict, err := tracker.HasConflict(ctx, sid, filePath2)
	require.NoError(t, err)
	require.True(t, conflict)
}
