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

// TestRepo_ForkFromEntry_PreservesMessageOrder verifies that the messages in a
// fork are stored in the same order as in the source session, regardless of the
// order in which msgRemap is iterated (Go maps have random iteration order).
func TestRepo_ForkFromEntry_PreservesMessageOrder(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	s := makeSession("tree-fork-order", "/project/order", "Order test")
	require.NoError(t, repo.Create(ctx, s))

	// Append five messages in a known order.
	texts := []string{"msg-0", "msg-1", "msg-2", "msg-3", "msg-4"}
	roles := []message.Role{
		message.RoleUser, message.RoleAssistant,
		message.RoleUser, message.RoleAssistant,
		message.RoleUser,
	}
	for i, text := range texts {
		require.NoError(t, repo.AppendMessage(ctx, s.ID, makeTextMsg(roles[i], text)))
	}
	srcMsgs, err := repo.Messages(ctx, s.ID)
	require.NoError(t, err)
	require.Len(t, srcMsgs, 5)

	// Build an explicit entry chain covering the first four messages.
	e0 := addEntry(t, ctx, repo, s.ID, nil, session.EntryMessage, &srcMsgs[0].ID, "")
	e1 := addEntry(t, ctx, repo, s.ID, &e0, session.EntryMessage, &srcMsgs[1].ID, "")
	e2 := addEntry(t, ctx, repo, s.ID, &e1, session.EntryMessage, &srcMsgs[2].ID, "")
	e3 := addEntry(t, ctx, repo, s.ID, &e2, session.EntryMessage, &srcMsgs[3].ID, "")
	// e4 is not on the fork path — only fork up to e3.

	fork, err := repo.ForkFromEntry(ctx, s.ID, e3, session.ForkFromEntryOptions{})
	require.NoError(t, err)
	require.Equal(t, 4, fork.MessageCount)

	forkMsgs, err := repo.Messages(ctx, fork.ID)
	require.NoError(t, err)
	require.Len(t, forkMsgs, 4)

	// The forked messages must be in the same order as the source (oldest first).
	for i := 0; i < 4; i++ {
		require.Equal(t, textOf(t, srcMsgs[i]), textOf(t, forkMsgs[i]),
			"fork message %d must match source message %d", i, i)
		require.Equal(t, roles[i], forkMsgs[i].Role,
			"fork message %d role must match source", i)
	}
}

// TestRepo_ImplicitTree_AppendThenGetPathAndBranch verifies that after
// appending messages (without calling AddEntry), GetPathToRoot and GetBranch
// fall back to the synthetic linear chain and return the correct entries.
func TestRepo_ImplicitTree_AppendThenGetPathAndBranch(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	s := makeSession("tree-implicit", "/project/implicit", "Implicit tree")
	require.NoError(t, repo.Create(ctx, s))

	// Append three messages without recording any explicit tree entries.
	require.NoError(t, repo.AppendMessage(ctx, s.ID, makeTextMsg(message.RoleUser, "q1")))
	require.NoError(t, repo.AppendMessage(ctx, s.ID, makeTextMsg(message.RoleAssistant, "a1")))
	require.NoError(t, repo.AppendMessage(ctx, s.ID, makeTextMsg(message.RoleUser, "q2")))

	msgs, err := repo.Messages(ctx, s.ID)
	require.NoError(t, err)
	require.Len(t, msgs, 3)

	// No explicit entries have been recorded.
	explicit, err := repo.Entries(ctx, s.ID)
	require.NoError(t, err)
	require.Empty(t, explicit)

	// ImplicitEntryID lets us derive the synthetic entry IDs.
	e0ID := session.ImplicitEntryID(msgs[0].ID)
	e1ID := session.ImplicitEntryID(msgs[1].ID)
	e2ID := session.ImplicitEntryID(msgs[2].ID)

	// GetPathToRoot from the third (leaf) entry should return all three in
	// root-first order.
	path, err := repo.GetPathToRoot(ctx, s.ID, e2ID)
	require.NoError(t, err)
	require.Len(t, path, 3, "path from leaf to root must include all three entries")
	require.Equal(t, e0ID, path[0].ID, "first path entry must be the root")
	require.Equal(t, e1ID, path[1].ID)
	require.Equal(t, e2ID, path[2].ID)

	// Each entry must be an EntryMessage and point at the right message.
	for i, e := range path {
		require.Equal(t, session.EntryMessage, e.Type)
		require.NotNil(t, e.RefID)
		require.Equal(t, msgs[i].ID, *e.RefID)
	}

	// GetBranch from the root must return all three (the entire linear chain).
	branch, err := repo.GetBranch(ctx, s.ID, e0ID)
	require.NoError(t, err)
	require.Len(t, branch, 3, "branch from root in a linear chain must include all entries")
	require.Equal(t, e0ID, branch[0].ID)

	// GetBranch from the middle entry must return just the last two.
	sub, err := repo.GetBranch(ctx, s.ID, e1ID)
	require.NoError(t, err)
	require.Len(t, sub, 2)
	require.Equal(t, e1ID, sub[0].ID)
	require.Equal(t, e2ID, sub[1].ID)
}

// TestRepo_ImplicitTree_ForkFromEntry verifies that ForkFromEntry works on a
// session that has messages but no explicit tree entries (implicit tree).
func TestRepo_ImplicitTree_ForkFromEntry(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	s := makeSession("tree-implicit-fork", "/project/implicit-fork", "Implicit fork")
	require.NoError(t, repo.Create(ctx, s))

	require.NoError(t, repo.AppendMessage(ctx, s.ID, makeTextMsg(message.RoleUser, "first")))
	require.NoError(t, repo.AppendMessage(ctx, s.ID, makeTextMsg(message.RoleAssistant, "reply")))
	require.NoError(t, repo.AppendMessage(ctx, s.ID, makeTextMsg(message.RoleUser, "follow-up")))

	msgs, err := repo.Messages(ctx, s.ID)
	require.NoError(t, err)
	require.Len(t, msgs, 3)

	// Fork from the second message's implicit entry (leaving "follow-up" behind).
	forkEntryID := session.ImplicitEntryID(msgs[1].ID)
	fork, err := repo.ForkFromEntry(ctx, s.ID, forkEntryID, session.ForkFromEntryOptions{
		BranchSummary: "abandoned follow-up",
	})
	require.NoError(t, err)
	require.NotEqual(t, s.ID, fork.ID)
	require.Equal(t, 2, fork.MessageCount, "fork should carry the first two messages only")

	forkMsgs, err := repo.Messages(ctx, fork.ID)
	require.NoError(t, err)
	require.Len(t, forkMsgs, 2)
	require.Equal(t, textOf(t, msgs[0]), textOf(t, forkMsgs[0]))
	require.Equal(t, textOf(t, msgs[1]), textOf(t, forkMsgs[1]))

	// The fork's entry tree must have 2 message entries + 1 branch-summary.
	forkEntries, err := repo.Entries(ctx, fork.ID)
	require.NoError(t, err)
	require.Len(t, forkEntries, 3)
	var summaries int
	for _, e := range forkEntries {
		if e.Type == session.EntryBranchSummary {
			summaries++
			require.Equal(t, "abandoned follow-up", e.Summary)
		}
	}
	require.Equal(t, 1, summaries)

	// Source is untouched.
	srcMsgsAfter, err := repo.Messages(ctx, s.ID)
	require.NoError(t, err)
	require.Len(t, srcMsgsAfter, 3)
}
