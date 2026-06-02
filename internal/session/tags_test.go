package session_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/session"
	"github.com/stretchr/testify/require"
)

// TestRepo_Tags_AddListRemove is the core behavior: tagging a session with two
// tags, reading them back, listing the session by a tag, then removing one tag.
func TestRepo_Tags_AddListRemove(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	require.NoError(t, repo.Create(ctx, makeSession("s1", "/p", "S1")))

	require.NoError(t, repo.AddTag(ctx, "s1", "urgent"))
	require.NoError(t, repo.AddTag(ctx, "s1", "review"))

	// Tags returns both, sorted alphabetically.
	tags, err := repo.Tags(ctx, "s1")
	require.NoError(t, err)
	require.Equal(t, []string{"review", "urgent"}, tags)

	// ListByTag finds the session under each of its tags.
	byUrgent, err := repo.ListByTag(ctx, "urgent")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"s1"}, idsOf(byUrgent))

	byReview, err := repo.ListByTag(ctx, "review")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"s1"}, idsOf(byReview))

	// RemoveTag drops only the named tag.
	require.NoError(t, repo.RemoveTag(ctx, "s1", "urgent"))

	tags, err = repo.Tags(ctx, "s1")
	require.NoError(t, err)
	require.Equal(t, []string{"review"}, tags)

	// The removed tag no longer lists the session...
	byUrgent, err = repo.ListByTag(ctx, "urgent")
	require.NoError(t, err)
	require.Empty(t, idsOf(byUrgent))

	// ...but the remaining tag still does.
	byReview, err = repo.ListByTag(ctx, "review")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"s1"}, idsOf(byReview))
}

// TestRepo_AddTag_Idempotent verifies adding the same tag twice is a no-op,
// leaving a single tag rather than erroring.
func TestRepo_AddTag_Idempotent(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	require.NoError(t, repo.Create(ctx, makeSession("s1", "/p", "S1")))

	require.NoError(t, repo.AddTag(ctx, "s1", "dup"))
	require.NoError(t, repo.AddTag(ctx, "s1", "dup")) // second add is a no-op

	tags, err := repo.Tags(ctx, "s1")
	require.NoError(t, err)
	require.Equal(t, []string{"dup"}, tags)
}

// TestRepo_RemoveTag_AbsentTag_NoOp verifies removing a tag the session does
// not carry is a no-op, not an error.
func TestRepo_RemoveTag_AbsentTag_NoOp(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	require.NoError(t, repo.Create(ctx, makeSession("s1", "/p", "S1")))
	require.NoError(t, repo.AddTag(ctx, "s1", "keep"))

	require.NoError(t, repo.RemoveTag(ctx, "s1", "never-added"))

	tags, err := repo.Tags(ctx, "s1")
	require.NoError(t, err)
	require.Equal(t, []string{"keep"}, tags)
}

// TestRepo_Tags_UnknownSessionAndTag verifies the read paths tolerate unknown
// ids and tags: Tags on an unknown id returns empty without error, and
// ListByTag for a tag no session carries returns empty without error.
func TestRepo_Tags_UnknownSessionAndTag(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	require.NoError(t, repo.Create(ctx, makeSession("s1", "/p", "S1")))
	require.NoError(t, repo.AddTag(ctx, "s1", "real"))

	tags, err := repo.Tags(ctx, "ghost")
	require.NoError(t, err)
	require.Empty(t, tags)

	listed, err := repo.ListByTag(ctx, "nonexistent")
	require.NoError(t, err)
	require.Empty(t, idsOf(listed))
}

// TestRepo_AddRemoveTag_UnknownSession_ReturnsErrNotFound verifies AddTag and
// RemoveTag reject ids with no backing session.
func TestRepo_AddRemoveTag_UnknownSession_ReturnsErrNotFound(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	require.ErrorIs(t, repo.AddTag(ctx, "ghost", "x"), session.ErrNotFound)
	require.ErrorIs(t, repo.RemoveTag(ctx, "ghost", "x"), session.ErrNotFound)
}

// TestRepo_ListByTag_MultipleSessionsOrdered verifies ListByTag returns every
// session bearing a shared tag, ordered by UpdatedAt DESC like List/Search.
func TestRepo_ListByTag_MultipleSessionsOrdered(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	a := makeSession("a", "/p", "A")
	b := makeSession("b", "/p", "B")
	c := makeSession("c", "/p", "C")
	a.UpdatedAt = a.UpdatedAt.Add(-3_000_000_000) // -3s (oldest)
	b.UpdatedAt = b.UpdatedAt.Add(-2_000_000_000) // -2s
	c.UpdatedAt = c.UpdatedAt.Add(-1_000_000_000) // -1s (newest)
	require.NoError(t, repo.Create(ctx, a))
	require.NoError(t, repo.Create(ctx, b))
	require.NoError(t, repo.Create(ctx, c))

	require.NoError(t, repo.AddTag(ctx, "a", "shared"))
	require.NoError(t, repo.AddTag(ctx, "c", "shared"))
	require.NoError(t, repo.AddTag(ctx, "b", "other"))

	got, err := repo.ListByTag(ctx, "shared")
	require.NoError(t, err)
	// Newest-first: c (-1s) before a (-3s); b is not tagged "shared".
	require.Equal(t, []string{"c", "a"}, idsOf(got))

	// Returned values are fully populated sessions, not just ids.
	require.Equal(t, "C", got[0].Title)
	require.Equal(t, "/p", got[0].ProjectPath)
}

// TestRepo_Delete_ClearsTags verifies deleting a session removes its tag rows,
// so a deleted session never surfaces from ListByTag (no orphan rows) and a
// fresh session reusing the id starts with no tags.
func TestRepo_Delete_ClearsTags(t *testing.T) {
	ctx := context.Background()
	database := openTestDB(t)
	repo := session.NewRepo(database)

	require.NoError(t, repo.Create(ctx, makeSession("reuse", "/p", "First")))
	require.NoError(t, repo.AddTag(ctx, "reuse", "label"))
	require.NoError(t, repo.Delete(ctx, "reuse"))

	// No orphan tag rows remain.
	var count int
	require.NoError(t, database.QueryRowContext(
		ctx, "SELECT COUNT(*) FROM session_tags WHERE session_id = ?", "reuse",
	).Scan(&count))
	require.Equal(t, 0, count)

	// ListByTag does not surface the deleted session.
	listed, err := repo.ListByTag(ctx, "label")
	require.NoError(t, err)
	require.Empty(t, idsOf(listed))

	// A fresh session reusing the id inherits no tags.
	require.NoError(t, repo.Create(ctx, makeSession("reuse", "/p", "Second")))
	tags, err := repo.Tags(ctx, "reuse")
	require.NoError(t, err)
	require.Empty(t, tags)
}

// TestRepo_Tags_ReadPathDoesNotCreateTagsTable verifies that Tags and ListByTag
// on a database where nothing has ever been tagged do not create the
// session_tags side table (the read path stays read-only).
func TestRepo_Tags_ReadPathDoesNotCreateTagsTable(t *testing.T) {
	ctx := context.Background()
	database := openTestDB(t)
	repo := session.NewRepo(database)

	require.NoError(t, repo.Create(ctx, makeSession("s1", "/p", "S1")))

	_, err := repo.Tags(ctx, "s1")
	require.NoError(t, err)
	_, err = repo.ListByTag(ctx, "anything")
	require.NoError(t, err)

	// The side table must not exist yet: no tag write has run.
	var name string
	err = database.QueryRowContext(
		ctx,
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'session_tags'`,
	).Scan(&name)
	require.ErrorIs(t, err, sql.ErrNoRows)

	// Adding a tag then creates it.
	require.NoError(t, repo.AddTag(ctx, "s1", "now"))
	require.NoError(t, database.QueryRowContext(
		ctx,
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'session_tags'`,
	).Scan(&name))
	require.Equal(t, "session_tags", name)
}
