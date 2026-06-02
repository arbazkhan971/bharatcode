package session_test

import (
	"context"
	"testing"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/session"
	"github.com/stretchr/testify/require"
)

// appendStamped appends a single-text-block message with an explicit role,
// text, and CreatedAt, returning nothing. Controlling CreatedAt explicitly lets
// the merge tests assert ordering deterministically without sleeping.
func appendStamped(t *testing.T, ctx context.Context, repo *session.Repo, sessionID string, role message.Role, text string, when time.Time) {
	t.Helper()
	msg := message.Message{
		Role:      role,
		Content:   []message.ContentBlock{message.TextBlock{Text: text}},
		CreatedAt: when,
	}
	require.NoError(t, repo.AppendMessage(ctx, sessionID, msg))
}

// texts returns the first-text-block text of each message, in order.
func texts(t *testing.T, msgs []message.Message) []string {
	t.Helper()
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = textOf(t, m)
	}
	return out
}

// assertNoDanglingParents asserts every ParentID in msgs resolves to an ID
// present in msgs (or is nil), i.e. the transcript is internally consistent.
func assertNoDanglingParents(t *testing.T, msgs []message.Message) {
	t.Helper()
	ids := make(map[string]struct{}, len(msgs))
	for _, m := range msgs {
		ids[m.ID] = struct{}{}
	}
	for _, m := range msgs {
		if m.ParentID == nil {
			continue
		}
		_, ok := ids[*m.ParentID]
		require.True(t, ok, "message %s references missing parent %s", m.ID, *m.ParentID)
	}
}

// TestRepo_Merge_AppendsInOrder verifies the target ends with its own messages
// followed by the source's, in order, and that source copies get fresh IDs
// while the target's own message IDs are untouched.
func TestRepo_Merge_AppendsInOrder(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	into := makeSession("into-1", "/project/merge", "Target session")
	require.NoError(t, repo.Create(ctx, into))
	appendStamped(t, ctx, repo, into.ID, message.RoleUser, "A", base)
	appendStamped(t, ctx, repo, into.ID, message.RoleAssistant, "B", base.Add(1*time.Second))

	from := makeSession("from-1", "/project/merge", "Source session")
	require.NoError(t, repo.Create(ctx, from))
	appendStamped(t, ctx, repo, from.ID, message.RoleUser, "C", base.Add(10*time.Second))
	appendStamped(t, ctx, repo, from.ID, message.RoleAssistant, "D", base.Add(11*time.Second))

	intoMsgsBefore, err := repo.Messages(ctx, into.ID)
	require.NoError(t, err)
	intoIDsBefore := []string{intoMsgsBefore[0].ID, intoMsgsBefore[1].ID}
	fromMsgsBefore, err := repo.Messages(ctx, from.ID)
	require.NoError(t, err)

	updated, err := repo.Merge(ctx, into.ID, from.ID, session.MergeOptions{})
	require.NoError(t, err)
	require.Equal(t, 4, updated.MessageCount)

	merged, err := repo.Messages(ctx, into.ID)
	require.NoError(t, err)
	require.Len(t, merged, 4)
	require.Equal(t, []string{"A", "B", "C", "D"}, texts(t, merged))

	// Target's own message IDs are unchanged.
	require.Equal(t, intoIDsBefore[0], merged[0].ID)
	require.Equal(t, intoIDsBefore[1], merged[1].ID)

	// Copied source messages received fresh IDs.
	require.NotEqual(t, fromMsgsBefore[0].ID, merged[2].ID)
	require.NotEqual(t, fromMsgsBefore[1].ID, merged[3].ID)
	require.NotEmpty(t, merged[2].ID)
	require.NotEmpty(t, merged[3].ID)

	// Denormalized count read back via Get matches.
	got, err := repo.Get(ctx, into.ID)
	require.NoError(t, err)
	require.Equal(t, 4, got.MessageCount)

	assertNoDanglingParents(t, merged)
}

// TestRepo_Merge_RestampOrdersSourceAfterTarget verifies the source block sorts
// after the target block even when the source's original timestamps predate the
// target's, because Merge re-stamps the copies.
func TestRepo_Merge_RestampOrdersSourceAfterTarget(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	into := makeSession("into-2", "/p", "Target")
	require.NoError(t, repo.Create(ctx, into))
	// Target messages are stamped LATER than the source messages on purpose.
	appendStamped(t, ctx, repo, into.ID, message.RoleUser, "T1", base.Add(100*time.Second))
	appendStamped(t, ctx, repo, into.ID, message.RoleAssistant, "T2", base.Add(101*time.Second))

	from := makeSession("from-2", "/p", "Source")
	require.NoError(t, repo.Create(ctx, from))
	appendStamped(t, ctx, repo, from.ID, message.RoleUser, "S1", base)
	appendStamped(t, ctx, repo, from.ID, message.RoleAssistant, "S2", base.Add(1*time.Second))

	_, err := repo.Merge(ctx, into.ID, from.ID, session.MergeOptions{})
	require.NoError(t, err)

	merged, err := repo.Messages(ctx, into.ID)
	require.NoError(t, err)
	// Despite the source's earlier original timestamps, it must come after the
	// target in the merged transcript.
	require.Equal(t, []string{"T1", "T2", "S1", "S2"}, texts(t, merged))
}

// TestRepo_Merge_IntoEmptyTargetPreservesTimestamps verifies that merging into
// a target with no messages preserves the source copies' original timestamps
// (rather than collapsing them to the zero time) while keeping their order.
func TestRepo_Merge_IntoEmptyTargetPreservesTimestamps(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	into := makeSession("into-empty", "/p", "Empty target")
	require.NoError(t, repo.Create(ctx, into))

	from := makeSession("from-empty", "/p", "Source")
	require.NoError(t, repo.Create(ctx, from))
	s1At := base
	s2At := base.Add(5 * time.Second)
	appendStamped(t, ctx, repo, from.ID, message.RoleUser, "S1", s1At)
	appendStamped(t, ctx, repo, from.ID, message.RoleAssistant, "S2", s2At)

	updated, err := repo.Merge(ctx, into.ID, from.ID, session.MergeOptions{})
	require.NoError(t, err)
	require.Equal(t, 2, updated.MessageCount)

	merged, err := repo.Messages(ctx, into.ID)
	require.NoError(t, err)
	require.Equal(t, []string{"S1", "S2"}, texts(t, merged))

	// Timestamps preserved (second granularity), not collapsed to year 1.
	require.True(t, merged[0].CreatedAt.Equal(s1At), "S1 stamp: got %s want %s", merged[0].CreatedAt, s1At)
	require.True(t, merged[1].CreatedAt.Equal(s2At), "S2 stamp: got %s want %s", merged[1].CreatedAt, s2At)
}

// TestRepo_Merge_RemapsParentLinks verifies intra-source parent links are
// remapped to the copied IDs and the merged transcript has no dangling parents.
func TestRepo_Merge_RemapsParentLinks(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	into := makeSession("into-3", "/p", "Target")
	require.NoError(t, repo.Create(ctx, into))
	appendStamped(t, ctx, repo, into.ID, message.RoleUser, "A", base)

	from := makeSession("from-3", "/p", "Source")
	require.NoError(t, repo.Create(ctx, from))
	// First source message has no parent; second points at the first.
	appendStamped(t, ctx, repo, from.ID, message.RoleUser, "S1", base.Add(10*time.Second))
	srcMsgs, err := repo.Messages(ctx, from.ID)
	require.NoError(t, err)
	parent := srcMsgs[0].ID
	require.NoError(t, repo.AppendMessage(ctx, from.ID, message.Message{
		Role:      message.RoleAssistant,
		Content:   []message.ContentBlock{message.TextBlock{Text: "S2"}},
		ParentID:  &parent,
		CreatedAt: base.Add(11 * time.Second),
	}))

	_, err = repo.Merge(ctx, into.ID, from.ID, session.MergeOptions{})
	require.NoError(t, err)

	merged, err := repo.Messages(ctx, into.ID)
	require.NoError(t, err)
	require.Equal(t, []string{"A", "S1", "S2"}, texts(t, merged))

	// S2's parent must now point at the copied S1, not the original source row.
	s1Copy, s2Copy := merged[1], merged[2]
	require.NotNil(t, s2Copy.ParentID)
	require.Equal(t, s1Copy.ID, *s2Copy.ParentID)
	require.NotEqual(t, parent, *s2Copy.ParentID, "parent must be remapped to the copied ID")

	assertNoDanglingParents(t, merged)
}

// TestRepo_Merge_DefaultLeavesSourceIntact verifies the zero MergeOptions
// leaves the source session and its messages untouched.
func TestRepo_Merge_DefaultLeavesSourceIntact(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	into := makeSession("into-4", "/p", "Target")
	require.NoError(t, repo.Create(ctx, into))
	appendStamped(t, ctx, repo, into.ID, message.RoleUser, "A", base)

	from := makeSession("from-4", "/p", "Source")
	require.NoError(t, repo.Create(ctx, from))
	appendStamped(t, ctx, repo, from.ID, message.RoleUser, "S1", base.Add(10*time.Second))
	appendStamped(t, ctx, repo, from.ID, message.RoleAssistant, "S2", base.Add(11*time.Second))

	_, err := repo.Merge(ctx, into.ID, from.ID, session.MergeOptions{})
	require.NoError(t, err)

	// Source still retrievable with all its messages.
	srcAfter, err := repo.Get(ctx, from.ID)
	require.NoError(t, err)
	require.Equal(t, 2, srcAfter.MessageCount)
	srcMsgs, err := repo.Messages(ctx, from.ID)
	require.NoError(t, err)
	require.Equal(t, []string{"S1", "S2"}, texts(t, srcMsgs))

	archived, err := repo.IsArchived(ctx, from.ID)
	require.NoError(t, err)
	require.False(t, archived)
}

// TestRepo_Merge_ArchiveSource verifies the source is archived after merge.
func TestRepo_Merge_ArchiveSource(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	into := makeSession("into-5", "/p", "Target")
	require.NoError(t, repo.Create(ctx, into))
	appendStamped(t, ctx, repo, into.ID, message.RoleUser, "A", base)

	from := makeSession("from-5", "/p", "Source")
	require.NoError(t, repo.Create(ctx, from))
	appendStamped(t, ctx, repo, from.ID, message.RoleUser, "S1", base.Add(10*time.Second))

	_, err := repo.Merge(ctx, into.ID, from.ID, session.MergeOptions{Disposition: session.ArchiveSource})
	require.NoError(t, err)

	// Source archived: still gettable, but hidden from default List.
	archived, err := repo.IsArchived(ctx, from.ID)
	require.NoError(t, err)
	require.True(t, archived)

	_, err = repo.Get(ctx, from.ID)
	require.NoError(t, err)

	active, err := repo.List(ctx, session.ListFilter{})
	require.NoError(t, err)
	for _, s := range active {
		require.NotEqual(t, from.ID, s.ID, "archived source must not appear in default List")
	}
}

// TestRepo_Merge_DeleteSource verifies the source is hard-deleted after merge.
func TestRepo_Merge_DeleteSource(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	into := makeSession("into-6", "/p", "Target")
	require.NoError(t, repo.Create(ctx, into))
	appendStamped(t, ctx, repo, into.ID, message.RoleUser, "A", base)

	from := makeSession("from-6", "/p", "Source")
	require.NoError(t, repo.Create(ctx, from))
	appendStamped(t, ctx, repo, from.ID, message.RoleUser, "S1", base.Add(10*time.Second))
	appendStamped(t, ctx, repo, from.ID, message.RoleAssistant, "S2", base.Add(11*time.Second))

	updated, err := repo.Merge(ctx, into.ID, from.ID, session.MergeOptions{Disposition: session.DeleteSource})
	require.NoError(t, err)
	require.Equal(t, 3, updated.MessageCount)

	// Merged transcript intact on the target.
	merged, err := repo.Messages(ctx, into.ID)
	require.NoError(t, err)
	require.Equal(t, []string{"A", "S1", "S2"}, texts(t, merged))

	// Source gone: Get is ErrNotFound and its messages are removed.
	_, err = repo.Get(ctx, from.ID)
	require.ErrorIs(t, err, session.ErrNotFound)
	srcMsgs, err := repo.Messages(ctx, from.ID)
	require.NoError(t, err)
	require.Empty(t, srcMsgs)
}

// TestRepo_Merge_UnknownSessions verifies missing target or source yields
// ErrNotFound, and self-merge is rejected.
func TestRepo_Merge_UnknownSessions(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	into := makeSession("into-7", "/p", "Target")
	require.NoError(t, repo.Create(ctx, into))

	_, err := repo.Merge(ctx, "missing-target", into.ID, session.MergeOptions{})
	require.ErrorIs(t, err, session.ErrNotFound)

	_, err = repo.Merge(ctx, into.ID, "missing-source", session.MergeOptions{})
	require.ErrorIs(t, err, session.ErrNotFound)

	_, err = repo.Merge(ctx, into.ID, into.ID, session.MergeOptions{})
	require.Error(t, err)
}
