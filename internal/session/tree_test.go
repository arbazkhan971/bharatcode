package session_test

import (
	"context"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/session"
	"github.com/stretchr/testify/require"
)

// addEntry is a test helper that appends an entry of the given type/parent and
// fails on error, returning the stored entry's ID.
func addEntry(t *testing.T, ctx context.Context, repo *session.Repo, sessionID string, parent *string, typ session.EntryType, ref *string, summary string) string {
	t.Helper()
	e, err := repo.AddEntry(ctx, &session.Entry{
		SessionID: sessionID,
		ParentID:  parent,
		Type:      typ,
		RefID:     ref,
		Summary:   summary,
	})
	require.NoError(t, err)
	require.NotEmpty(t, e.ID)
	return e.ID
}

// TestRepo_AddEntry_AndList verifies entries persist and list oldest-first.
func TestRepo_AddEntry_AndList(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	s := makeSession("tree-1", "/project/tree", "Tree session")
	require.NoError(t, repo.Create(ctx, s))

	root := addEntry(t, ctx, repo, s.ID, nil, session.EntryMessage, nil, "")
	mid := addEntry(t, ctx, repo, s.ID, &root, session.EntryModelChange, nil, "kimi-k2")
	addEntry(t, ctx, repo, s.ID, &mid, session.EntryCompaction, nil, "")

	entries, err := repo.Entries(ctx, s.ID)
	require.NoError(t, err)
	require.Len(t, entries, 3)
	require.Equal(t, root, entries[0].ID)
	require.Nil(t, entries[0].ParentID)
	require.Equal(t, session.EntryModelChange, entries[1].Type)
	require.Equal(t, "kimi-k2", entries[1].Summary)
	require.Equal(t, mid, *entries[2].ParentID)
}

// TestRepo_Entries_EmptyBeforeAnyWrite verifies a session with no entries (and a
// database where nothing has ever been entered) returns an empty slice.
func TestRepo_Entries_EmptyBeforeAnyWrite(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	s := makeSession("tree-empty", "/p", "Empty")
	require.NoError(t, repo.Create(ctx, s))

	entries, err := repo.Entries(ctx, s.ID)
	require.NoError(t, err)
	require.Empty(t, entries)
	require.NotNil(t, entries)
}

// TestRepo_AddEntry_Validation covers the three rejection paths: unknown
// session, unknown type, and a parent that is not in the session.
func TestRepo_AddEntry_Validation(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	s := makeSession("tree-val", "/p", "Val")
	require.NoError(t, repo.Create(ctx, s))

	// Unknown session.
	_, err := repo.AddEntry(ctx, &session.Entry{SessionID: "nope", Type: session.EntryMessage})
	require.ErrorIs(t, err, session.ErrNotFound)

	// Unknown type.
	_, err = repo.AddEntry(ctx, &session.Entry{SessionID: s.ID, Type: session.EntryType("bogus")})
	require.Error(t, err)
	require.NotErrorIs(t, err, session.ErrNotFound)

	// Parent not in session.
	bogusParent := "missing-parent"
	_, err = repo.AddEntry(ctx, &session.Entry{SessionID: s.ID, Type: session.EntryMessage, ParentID: &bogusParent})
	require.ErrorIs(t, err, session.ErrNotFound)
}

// TestRepo_GetEntry covers a hit and a miss.
func TestRepo_GetEntry(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	s := makeSession("tree-get", "/p", "Get")
	require.NoError(t, repo.Create(ctx, s))
	id := addEntry(t, ctx, repo, s.ID, nil, session.EntryMessage, nil, "")

	got, err := repo.GetEntry(ctx, s.ID, id)
	require.NoError(t, err)
	require.Equal(t, id, got.ID)

	_, err = repo.GetEntry(ctx, s.ID, "does-not-exist")
	require.ErrorIs(t, err, session.ErrNotFound)
}

// TestRepo_GetPathToRoot verifies the path is root-first and stops at the
// selected node, ignoring sibling branches.
func TestRepo_GetPathToRoot(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	s := makeSession("tree-path", "/p", "Path")
	require.NoError(t, repo.Create(ctx, s))

	// root -> a -> b ; root -> c (a sibling branch that must not appear).
	root := addEntry(t, ctx, repo, s.ID, nil, session.EntryMessage, nil, "root")
	a := addEntry(t, ctx, repo, s.ID, &root, session.EntryMessage, nil, "a")
	b := addEntry(t, ctx, repo, s.ID, &a, session.EntryMessage, nil, "b")
	addEntry(t, ctx, repo, s.ID, &root, session.EntryMessage, nil, "c")

	path, err := repo.GetPathToRoot(ctx, s.ID, b)
	require.NoError(t, err)
	require.Len(t, path, 3)
	require.Equal(t, root, path[0].ID)
	require.Equal(t, a, path[1].ID)
	require.Equal(t, b, path[2].ID)

	_, err = repo.GetPathToRoot(ctx, s.ID, "nope")
	require.ErrorIs(t, err, session.ErrNotFound)
}

// TestRepo_GetBranch verifies the subtree rooted at a node includes the node and
// all descendants but excludes ancestors and unrelated branches.
func TestRepo_GetBranch(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	s := makeSession("tree-branch", "/p", "Branch")
	require.NoError(t, repo.Create(ctx, s))

	// root -> a -> {b, c -> d} ; root -> e (unrelated).
	root := addEntry(t, ctx, repo, s.ID, nil, session.EntryMessage, nil, "root")
	a := addEntry(t, ctx, repo, s.ID, &root, session.EntryMessage, nil, "a")
	b := addEntry(t, ctx, repo, s.ID, &a, session.EntryMessage, nil, "b")
	c := addEntry(t, ctx, repo, s.ID, &a, session.EntryMessage, nil, "c")
	d := addEntry(t, ctx, repo, s.ID, &c, session.EntryMessage, nil, "d")
	e := addEntry(t, ctx, repo, s.ID, &root, session.EntryMessage, nil, "e")

	branch, err := repo.GetBranch(ctx, s.ID, a)
	require.NoError(t, err)

	got := make(map[string]bool, len(branch))
	for _, en := range branch {
		got[en.ID] = true
	}
	require.True(t, got[a])
	require.True(t, got[b])
	require.True(t, got[c])
	require.True(t, got[d])
	require.False(t, got[root], "ancestor must not be in the branch")
	require.False(t, got[e], "unrelated branch must not be in the branch")
	require.Equal(t, a, branch[0].ID, "branch starts at its root node")
}

// TestRepo_ForkFromEntry copies the lineage to a new session, carries the
// referenced messages, and leaves the source untouched.
func TestRepo_ForkFromEntry(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	s := makeSession("tree-fork-src", "/project/fork", "Exploration")
	require.NoError(t, repo.Create(ctx, s))

	// Seed three messages and a corresponding entry chain referencing them.
	require.NoError(t, repo.AppendMessage(ctx, s.ID, makeTextMsg(message.RoleUser, "q1")))
	require.NoError(t, repo.AppendMessage(ctx, s.ID, makeTextMsg(message.RoleAssistant, "a1")))
	require.NoError(t, repo.AppendMessage(ctx, s.ID, makeTextMsg(message.RoleUser, "q2")))
	srcMsgs, err := repo.Messages(ctx, s.ID)
	require.NoError(t, err)
	require.Len(t, srcMsgs, 3)

	e0 := addEntry(t, ctx, repo, s.ID, nil, session.EntryMessage, &srcMsgs[0].ID, "")
	e1 := addEntry(t, ctx, repo, s.ID, &e0, session.EntryMessage, &srcMsgs[1].ID, "")
	// e2 is the abandoned tail: it must NOT be carried when forking from e1.
	addEntry(t, ctx, repo, s.ID, &e1, session.EntryMessage, &srcMsgs[2].ID, "")

	fork, err := repo.ForkFromEntry(ctx, s.ID, e1, session.ForkFromEntryOptions{
		BranchSummary: "tried q2 first; abandoned",
	})
	require.NoError(t, err)
	require.NotEqual(t, s.ID, fork.ID)
	require.Equal(t, "Exploration (fork)", fork.Title)
	require.Equal(t, "/project/fork", fork.ProjectPath)
	require.NotNil(t, fork.OriginSessionID)
	require.Equal(t, s.ID, *fork.OriginSessionID)
	// Two message entries on the path => two messages carried.
	require.Equal(t, 2, fork.MessageCount)

	// The fork carries the lineage (e0, e1) plus the branch-summary node.
	forkEntries, err := repo.Entries(ctx, fork.ID)
	require.NoError(t, err)
	require.Len(t, forkEntries, 3)

	var summaries int
	var msgEntries []session.Entry
	for _, e := range forkEntries {
		switch e.Type {
		case session.EntryBranchSummary:
			summaries++
			require.Equal(t, "tried q2 first; abandoned", e.Summary)
			require.NotNil(t, e.RefID)
			require.Equal(t, e1, *e.RefID, "branch summary points back at the source fork entry")
		case session.EntryMessage:
			msgEntries = append(msgEntries, e)
		}
	}
	require.Equal(t, 1, summaries)
	require.Len(t, msgEntries, 2)

	// The carried messages are fresh rows pointing at the fork, content-equal to
	// the source's first two messages.
	forkMsgs, err := repo.Messages(ctx, fork.ID)
	require.NoError(t, err)
	require.Len(t, forkMsgs, 2)
	require.Equal(t, textOf(t, srcMsgs[0]), textOf(t, forkMsgs[0]))
	require.Equal(t, textOf(t, srcMsgs[1]), textOf(t, forkMsgs[1]))
	for i := range forkMsgs {
		require.NotEqual(t, srcMsgs[i].ID, forkMsgs[i].ID)
	}

	// The source session is unchanged: still three messages and three entries.
	srcEntries, err := repo.Entries(ctx, s.ID)
	require.NoError(t, err)
	require.Len(t, srcEntries, 3)
	srcMsgsAfter, err := repo.Messages(ctx, s.ID)
	require.NoError(t, err)
	require.Len(t, srcMsgsAfter, 3)
}

// TestRepo_ForkFromEntry_NoSummary forks without a branch summary, so only the
// lineage entries are carried.
func TestRepo_ForkFromEntry_NoSummary(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	s := makeSession("tree-fork-nosum", "/p", "S")
	require.NoError(t, repo.Create(ctx, s))
	root := addEntry(t, ctx, repo, s.ID, nil, session.EntryCompaction, nil, "")
	leaf := addEntry(t, ctx, repo, s.ID, &root, session.EntryModelChange, nil, "kimi-k2")

	fork, err := repo.ForkFromEntry(ctx, s.ID, leaf, session.ForkFromEntryOptions{Title: "Branch B"})
	require.NoError(t, err)
	require.Equal(t, "Branch B", fork.Title)
	require.Equal(t, 0, fork.MessageCount)

	forkEntries, err := repo.Entries(ctx, fork.ID)
	require.NoError(t, err)
	require.Len(t, forkEntries, 2)
	for _, e := range forkEntries {
		require.NotEqual(t, session.EntryBranchSummary, e.Type)
	}
}

// TestRepo_ForkFromEntry_Unknown covers the not-found paths for both an unknown
// source session and an unknown entry.
func TestRepo_ForkFromEntry_Unknown(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	_, err := repo.ForkFromEntry(ctx, "no-session", "no-entry", session.ForkFromEntryOptions{})
	require.ErrorIs(t, err, session.ErrNotFound)

	s := makeSession("tree-fork-unk", "/p", "S")
	require.NoError(t, repo.Create(ctx, s))
	_, err = repo.ForkFromEntry(ctx, s.ID, "no-entry", session.ForkFromEntryOptions{})
	require.ErrorIs(t, err, session.ErrNotFound)
}
