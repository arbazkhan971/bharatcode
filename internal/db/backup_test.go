package db

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	sqlc "github.com/arbazkhan971/bharatcode/internal/db/sqlc"
	"github.com/stretchr/testify/require"
)

// TestBackupAndRestore exercises the real backup path: it seeds a source
// database with sessions and messages, backs it up to a temp path, opens
// that backup as an independent BharatCode database, and asserts the rows
// are present, queries work, and the restored database is still writable.
func TestBackupAndRestore(t *testing.T) {
	ctx := context.Background()
	srcPath := filepath.Join(t.TempDir(), "source.db")

	src, err := Open(ctx, srcPath)
	require.NoError(t, err)
	defer src.Close()

	// Seed a known set of sessions, each with a message, so we can assert
	// exact counts and round-trip a specific row through the backup.
	const rows = 25
	for i := 0; i < rows; i++ {
		sid := fmt.Sprintf("session-%d", i)
		_, err = src.Queries.CreateSession(ctx, sqlc.CreateSessionParams{
			ID:          sid,
			ProjectPath: "/path",
			Title:       fmt.Sprintf("Session %d", i),
			Model:       "gpt-4",
			Agent:       "coder",
			CreatedAt:   12345,
			UpdatedAt:   12345,
		})
		require.NoError(t, err)

		_, err = src.Queries.CreateMessage(ctx, sqlc.CreateMessageParams{
			ID:          fmt.Sprintf("message-%d", i),
			SessionID:   sid,
			Role:        "user",
			ContentJson: `{"text": "payload"}`,
			ParentID:    nil,
			CreatedAt:   12345,
		})
		require.NoError(t, err)
	}

	// Back up to a fresh path (must not pre-exist for VACUUM INTO).
	backupPath := filepath.Join(t.TempDir(), "backup.db")
	require.NoError(t, src.Backup(ctx, backupPath))

	info, err := os.Stat(backupPath)
	require.NoError(t, err, "backup file should exist")
	require.Positive(t, info.Size(), "backup file should be non-empty")

	// Open the backup as a wholly independent database via Restore.
	restored, err := Restore(ctx, backupPath)
	require.NoError(t, err)
	defer restored.Close()
	require.NotEqual(t, src.Path(), restored.Path(), "restored db is a distinct file")

	// The backup is itself a valid, consistent BharatCode database.
	require.NoError(t, restored.IntegrityCheck(ctx))

	// Every seeded session round-trips through the backup.
	var sessionCount int
	err = restored.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions").Scan(&sessionCount)
	require.NoError(t, err)
	require.Equal(t, rows, sessionCount, "all sessions must be present in the backup")

	var messageCount int
	err = restored.QueryRowContext(ctx, "SELECT COUNT(*) FROM messages").Scan(&messageCount)
	require.NoError(t, err)
	require.Equal(t, rows, messageCount, "all messages must be present in the backup")

	// A specific row reads back through the generated queries.
	got, err := restored.Queries.GetSessionByID(ctx, "session-7")
	require.NoError(t, err)
	require.Equal(t, "session-7", got.ID)
	require.Equal(t, "Session 7", got.Title)

	// The restored database is fully usable for writes too.
	_, err = restored.Queries.CreateSession(ctx, sqlc.CreateSessionParams{
		ID:          "session-after-restore",
		ProjectPath: "/path",
		Title:       "After Restore",
		Model:       "gpt-4",
		Agent:       "coder",
		CreatedAt:   23456,
		UpdatedAt:   23456,
	})
	require.NoError(t, err)

	err = restored.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions").Scan(&sessionCount)
	require.NoError(t, err)
	require.Equal(t, rows+1, sessionCount)

	// Writing to the restored copy must not have touched the source.
	var srcCount int
	err = src.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions").Scan(&srcCount)
	require.NoError(t, err)
	require.Equal(t, rows, srcCount, "backup is independent of the source")
}

// TestBackupClosedDB asserts Backup refuses to run on a closed database,
// satisfying the closed-DB guard requirement.
func TestBackupClosedDB(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test_backup_closed.db")

	d, err := Open(ctx, dbPath)
	require.NoError(t, err)

	require.NoError(t, d.Close())

	require.ErrorIs(t, d.Backup(ctx, filepath.Join(t.TempDir(), "backup.db")), ErrClosed)
}

// TestBackupRejectsExistingDestination asserts Backup does not silently
// overwrite an existing file, making the overwrite semantics intentional.
func TestBackupRejectsExistingDestination(t *testing.T) {
	ctx := context.Background()
	d, err := Open(ctx, filepath.Join(t.TempDir(), "source.db"))
	require.NoError(t, err)
	defer d.Close()

	dest := filepath.Join(t.TempDir(), "occupied.db")
	require.NoError(t, os.WriteFile(dest, []byte("pre-existing"), 0o644))

	require.Error(t, d.Backup(ctx, dest), "backup must not overwrite an existing file")
}
