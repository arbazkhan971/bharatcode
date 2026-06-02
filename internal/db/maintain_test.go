package db

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	sqlc "github.com/arbazkhan971/bharatcode/internal/db/sqlc"
	"github.com/stretchr/testify/require"
)

// TestMaintain verifies the real maintenance path: it seeds and then
// deletes rows to create free pages, runs Maintain, and asserts the
// integrity check passed (Maintain returns nil), that VACUUM actually
// reclaimed the free pages (freelist_count drops to 0), and that the
// database is still fully usable for both reads and writes afterward.
func TestMaintain(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test_maintain.db")

	d, err := Open(ctx, dbPath)
	require.NoError(t, err)
	defer d.Close()

	_, err = d.Queries.CreateSession(ctx, sqlc.CreateSessionParams{
		ID:          "session-keep",
		ProjectPath: "/path",
		Title:       "Keeper",
		Model:       "gpt-4",
		Agent:       "coder",
		CreatedAt:   12345,
		UpdatedAt:   12345,
	})
	require.NoError(t, err)

	// Write a batch of sessions plus messages, then delete most of them so
	// the file accumulates free pages that VACUUM must reclaim.
	const churn = 200
	for i := 0; i < churn; i++ {
		sid := fmt.Sprintf("session-%d", i)
		_, err = d.Queries.CreateSession(ctx, sqlc.CreateSessionParams{
			ID:          sid,
			ProjectPath: "/path",
			Title:       fmt.Sprintf("Session %d", i),
			Model:       "gpt-4",
			Agent:       "coder",
			CreatedAt:   12345,
			UpdatedAt:   12345,
		})
		require.NoError(t, err)

		_, err = d.Queries.CreateMessage(ctx, sqlc.CreateMessageParams{
			ID:          fmt.Sprintf("message-%d", i),
			SessionID:   sid,
			Role:        "user",
			ContentJson: `{"text": "some reasonably sized payload to occupy pages"}`,
			ParentID:    nil,
			CreatedAt:   12345,
		})
		require.NoError(t, err)
	}

	for i := 0; i < churn; i++ {
		err = d.Queries.DeleteSession(ctx, fmt.Sprintf("session-%d", i))
		require.NoError(t, err)
	}

	// After the deletes there should be free pages waiting to be reclaimed.
	var freeBefore int
	err = d.QueryRowContext(ctx, "PRAGMA freelist_count;").Scan(&freeBefore)
	require.NoError(t, err)
	require.Positive(t, freeBefore, "deletes should have produced free pages for VACUUM to reclaim")

	require.NoError(t, d.Maintain(ctx))

	// VACUUM rebuilds the file with no free pages.
	var freeAfter int
	err = d.QueryRowContext(ctx, "PRAGMA freelist_count;").Scan(&freeAfter)
	require.NoError(t, err)
	require.Zero(t, freeAfter, "VACUUM should have reclaimed all free pages")

	// The file is still consistent after VACUUM rebuilt it.
	require.NoError(t, d.IntegrityCheck(ctx))

	// The database must still be usable: the kept session reads back...
	got, err := d.Queries.GetSessionByID(ctx, "session-keep")
	require.NoError(t, err)
	require.Equal(t, "session-keep", got.ID)

	var count int
	err = d.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count, "only the kept session should remain after maintenance")

	// ...and writes still succeed after maintenance.
	_, err = d.Queries.CreateSession(ctx, sqlc.CreateSessionParams{
		ID:          "session-after",
		ProjectPath: "/path",
		Title:       "After Maintenance",
		Model:       "gpt-4",
		Agent:       "coder",
		CreatedAt:   23456,
		UpdatedAt:   23456,
	})
	require.NoError(t, err)

	err = d.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 2, count)
}

// TestIntegrityCheck asserts IntegrityCheck reports a healthy freshly
// migrated database as consistent.
func TestIntegrityCheck(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test_integrity.db")

	d, err := Open(ctx, dbPath)
	require.NoError(t, err)
	defer d.Close()

	require.NoError(t, d.IntegrityCheck(ctx))
}

// TestMaintainClosedDB asserts both maintenance entry points refuse to run
// on a closed database, satisfying the closed-DB guard requirement.
func TestMaintainClosedDB(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test_maintain_closed.db")

	d, err := Open(ctx, dbPath)
	require.NoError(t, err)

	require.NoError(t, d.Close())

	require.ErrorIs(t, d.Maintain(ctx), ErrClosed)
	require.ErrorIs(t, d.IntegrityCheck(ctx), ErrClosed)
}
