package session_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/session"
	"github.com/stretchr/testify/require"
)

// idsOf returns the IDs of the sessions in order, for set/contains assertions.
func idsOf(sessions []session.Session) []string {
	ids := make([]string, len(sessions))
	for i, s := range sessions {
		ids[i] = s.ID
	}
	return ids
}

// TestRepo_Archive_HiddenFromDefaultList_ShownWithArchived is the core
// behavior: an archived session is excluded from the default List but appears
// when IncludeArchived is set, and remains retrievable via Get.
func TestRepo_Archive_HiddenFromDefaultList_ShownWithArchived(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	require.NoError(t, repo.Create(ctx, makeSession("keep", "/p", "Keep")))
	require.NoError(t, repo.Create(ctx, makeSession("arch", "/p", "Archive")))

	require.NoError(t, repo.Archive(ctx, "arch"))

	// Default List excludes the archived session.
	def, err := repo.List(ctx, session.ListFilter{})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"keep"}, idsOf(def))

	// List with IncludeArchived includes both.
	all, err := repo.List(ctx, session.ListFilter{IncludeArchived: true})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"keep", "arch"}, idsOf(all))

	// The archived session is still directly retrievable.
	got, err := repo.Get(ctx, "arch")
	require.NoError(t, err)
	require.Equal(t, "arch", got.ID)
	require.Equal(t, "Archive", got.Title)
}

// TestRepo_IsArchived_TracksState verifies IsArchived reflects archive and
// unarchive operations and reports false for unknown IDs.
func TestRepo_IsArchived_TracksState(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	require.NoError(t, repo.Create(ctx, makeSession("s1", "/p", "S1")))

	archived, err := repo.IsArchived(ctx, "s1")
	require.NoError(t, err)
	require.False(t, archived)

	require.NoError(t, repo.Archive(ctx, "s1"))
	archived, err = repo.IsArchived(ctx, "s1")
	require.NoError(t, err)
	require.True(t, archived)

	require.NoError(t, repo.Unarchive(ctx, "s1"))
	archived, err = repo.IsArchived(ctx, "s1")
	require.NoError(t, err)
	require.False(t, archived)

	// Unknown ID is reported as not archived without error.
	archived, err = repo.IsArchived(ctx, "does-not-exist")
	require.NoError(t, err)
	require.False(t, archived)
}

// TestRepo_Unarchive_RestoresToDefaultList verifies a session reappears in the
// default List after Unarchive.
func TestRepo_Unarchive_RestoresToDefaultList(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	require.NoError(t, repo.Create(ctx, makeSession("s1", "/p", "S1")))
	require.NoError(t, repo.Archive(ctx, "s1"))

	def, err := repo.List(ctx, session.ListFilter{})
	require.NoError(t, err)
	require.Empty(t, idsOf(def))

	require.NoError(t, repo.Unarchive(ctx, "s1"))

	def, err = repo.List(ctx, session.ListFilter{})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"s1"}, idsOf(def))
}

// TestRepo_Archive_Idempotent verifies archiving twice and unarchiving a
// non-archived session are no-ops, not errors.
func TestRepo_Archive_Idempotent(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	require.NoError(t, repo.Create(ctx, makeSession("s1", "/p", "S1")))

	require.NoError(t, repo.Archive(ctx, "s1"))
	require.NoError(t, repo.Archive(ctx, "s1")) // second archive is a no-op

	archived, err := repo.IsArchived(ctx, "s1")
	require.NoError(t, err)
	require.True(t, archived)

	require.NoError(t, repo.Unarchive(ctx, "s1"))
	require.NoError(t, repo.Unarchive(ctx, "s1")) // unarchiving twice is fine

	archived, err = repo.IsArchived(ctx, "s1")
	require.NoError(t, err)
	require.False(t, archived)
}

// TestRepo_Archive_UnknownSession_ReturnsErrNotFound verifies Archive and
// Unarchive reject IDs with no backing session.
func TestRepo_Archive_UnknownSession_ReturnsErrNotFound(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	require.ErrorIs(t, repo.Archive(ctx, "ghost"), session.ErrNotFound)
	require.ErrorIs(t, repo.Unarchive(ctx, "ghost"), session.ErrNotFound)
}

// TestRepo_Archive_RespectsLimitOverActiveSessions verifies that when archived
// sessions are hidden, Limit counts visible sessions: with one of three
// sessions archived, a Limit of 2 still returns 2 active sessions.
func TestRepo_Archive_RespectsLimitOverActiveSessions(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	// Insert with increasing UpdatedAt so ordering is deterministic. newest is
	// "c" (List orders by UpdatedAt DESC).
	a := makeSession("a", "/p", "A")
	b := makeSession("b", "/p", "B")
	c := makeSession("c", "/p", "C")
	a.UpdatedAt = a.UpdatedAt.Add(-3_000_000_000) // -3s
	b.UpdatedAt = b.UpdatedAt.Add(-2_000_000_000) // -2s
	c.UpdatedAt = c.UpdatedAt.Add(-1_000_000_000) // -1s
	require.NoError(t, repo.Create(ctx, a))
	require.NoError(t, repo.Create(ctx, b))
	require.NoError(t, repo.Create(ctx, c))

	// Archive the newest ("c"). With Limit 2, we must still get 2 active rows
	// ("b","a"), not just "b" (which a naive SQL-LIMIT-before-filter returns).
	require.NoError(t, repo.Archive(ctx, "c"))

	got, err := repo.List(ctx, session.ListFilter{Limit: 2})
	require.NoError(t, err)
	require.Equal(t, []string{"b", "a"}, idsOf(got))
}

// TestRepo_Delete_RemovesSessionAndMessages verifies Delete hard-deletes the
// session and cascades to its messages: both the session and its messages are
// gone afterward.
func TestRepo_Delete_RemovesSessionAndMessages(t *testing.T) {
	ctx := context.Background()
	database := openTestDB(t)
	repo := session.NewRepo(database)

	require.NoError(t, repo.Create(ctx, makeSession("d1", "/p", "Delete Me")))
	require.NoError(t, repo.AppendMessage(ctx, "d1", makeTextMsg(message.RoleUser, "hello")))
	require.NoError(t, repo.AppendMessage(ctx, "d1", makeTextMsg(message.RoleAssistant, "hi")))

	// Messages exist before delete.
	msgs, err := repo.Messages(ctx, "d1")
	require.NoError(t, err)
	require.Len(t, msgs, 2)

	require.NoError(t, repo.Delete(ctx, "d1"))

	// Session is gone.
	_, err = repo.Get(ctx, "d1")
	require.ErrorIs(t, err, session.ErrNotFound)

	// Messages are gone (verified via raw DB so we count actual rows, not a
	// derived count).
	var count int
	require.NoError(t, database.QueryRowContext(
		ctx, "SELECT COUNT(*) FROM messages WHERE session_id = ?", "d1",
	).Scan(&count))
	require.Equal(t, 0, count)
}

// TestRepo_Delete_ClearsArchiveMarker verifies deleting an archived session
// removes its archive marker, so a new session later reusing the same ID is
// not silently hidden from the default List.
func TestRepo_Delete_ClearsArchiveMarker(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	require.NoError(t, repo.Create(ctx, makeSession("reuse", "/p", "First")))
	require.NoError(t, repo.Archive(ctx, "reuse"))
	require.NoError(t, repo.Delete(ctx, "reuse"))

	// The marker must be gone.
	archived, err := repo.IsArchived(ctx, "reuse")
	require.NoError(t, err)
	require.False(t, archived)

	// A fresh session reusing the ID is visible in the default List.
	require.NoError(t, repo.Create(ctx, makeSession("reuse", "/p", "Second")))
	def, err := repo.List(ctx, session.ListFilter{})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"reuse"}, idsOf(def))
}

// TestRepo_Archive_ProjectFilterAndArchiveCombine verifies the archive filter
// composes with the ProjectPath filter rather than overriding it.
func TestRepo_Archive_ProjectFilterAndArchiveCombine(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	require.NoError(t, repo.Create(ctx, makeSession("pa1", "/proj-a", "A1")))
	require.NoError(t, repo.Create(ctx, makeSession("pa2", "/proj-a", "A2")))
	require.NoError(t, repo.Create(ctx, makeSession("pb1", "/proj-b", "B1")))

	require.NoError(t, repo.Archive(ctx, "pa2"))

	got, err := repo.List(ctx, session.ListFilter{ProjectPath: "/proj-a"})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"pa1"}, idsOf(got))

	gotAll, err := repo.List(ctx, session.ListFilter{ProjectPath: "/proj-a", IncludeArchived: true})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"pa1", "pa2"}, idsOf(gotAll))
}

// TestRepo_Search_ExcludesArchived verifies Search (which draws from the
// default List view) does not surface archived sessions, neither for an empty
// query (which returns every visible session) nor for a query that matches an
// archived session's title.
func TestRepo_Search_ExcludesArchived(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	require.NoError(t, repo.Create(ctx, makeSession("keep", "/p", "shared keyword keep")))
	require.NoError(t, repo.Create(ctx, makeSession("arch", "/p", "shared keyword arch")))

	require.NoError(t, repo.Archive(ctx, "arch"))

	// Empty query returns only the non-archived session.
	all, err := repo.Search(ctx, "")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"keep"}, idsOf(all))

	// A query matching both titles still excludes the archived one.
	matched, err := repo.Search(ctx, "shared keyword")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"keep"}, idsOf(matched))

	// After unarchiving, the session reappears in search results.
	require.NoError(t, repo.Unarchive(ctx, "arch"))
	matched, err = repo.Search(ctx, "shared keyword")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"keep", "arch"}, idsOf(matched))
}

// TestRepo_List_ReadPathDoesNotCreateArchiveTable verifies that a List call on
// a database where nothing has ever been archived does not create the
// session_archive side table (the read path stays read-only).
func TestRepo_List_ReadPathDoesNotCreateArchiveTable(t *testing.T) {
	ctx := context.Background()
	database := openTestDB(t)
	repo := session.NewRepo(database)

	require.NoError(t, repo.Create(ctx, makeSession("s1", "/p", "S1")))

	_, err := repo.List(ctx, session.ListFilter{})
	require.NoError(t, err)

	// The side table must not exist yet: no archive operation has run.
	var name string
	err = database.QueryRowContext(
		ctx,
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'session_archive'`,
	).Scan(&name)
	require.ErrorIs(t, err, sql.ErrNoRows)

	// Archiving then creates it, and List still works.
	require.NoError(t, repo.Archive(ctx, "s1"))
	require.NoError(t, database.QueryRowContext(
		ctx,
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'session_archive'`,
	).Scan(&name))
	require.Equal(t, "session_archive", name)
}
