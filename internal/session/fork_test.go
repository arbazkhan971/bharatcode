package session_test

import (
	"context"
	"testing"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/session"
	"github.com/stretchr/testify/require"
)

// textOf returns the text of the first TextBlock in m, failing the test if
// there is none.
func textOf(t *testing.T, m message.Message) string {
	t.Helper()
	for _, block := range m.Content {
		if tb, ok := block.(message.TextBlock); ok {
			return tb.Text
		}
		if tb, ok := block.(*message.TextBlock); ok {
			return tb.Text
		}
	}
	t.Fatalf("message %s has no text block", m.ID)
	return ""
}

// seedSource creates a session with three appended user/assistant/user
// messages and returns the source session ID along with the persisted
// messages (oldest first).
func seedSource(t *testing.T, ctx context.Context, repo *session.Repo) (string, []message.Message) {
	t.Helper()

	src := makeSession("src-1", "/project/fork", "Original exploration")
	require.NoError(t, repo.Create(ctx, src))

	require.NoError(t, repo.AppendMessage(ctx, src.ID, makeTextMsg(message.RoleUser, "first question")))
	require.NoError(t, repo.AppendMessage(ctx, src.ID, makeTextMsg(message.RoleAssistant, "first answer")))
	require.NoError(t, repo.AppendMessage(ctx, src.ID, makeTextMsg(message.RoleUser, "second question")))

	msgs, err := repo.Messages(ctx, src.ID)
	require.NoError(t, err)
	require.Len(t, msgs, 3)
	return src.ID, msgs
}

// TestRepo_Fork_NewIDAndCopiedMessages verifies the fork is a new session
// whose messages are content-identical copies of the source with new IDs.
func TestRepo_Fork_NewIDAndCopiedMessages(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	srcID, srcMsgs := seedSource(t, ctx, repo)

	fork, err := repo.Fork(ctx, srcID, session.ForkOptions{})
	require.NoError(t, err)

	// (a) The fork is a new session with its own ID.
	require.NotEmpty(t, fork.ID)
	require.NotEqual(t, srcID, fork.ID)
	require.Equal(t, "Original exploration (fork)", fork.Title)
	require.Equal(t, "/project/fork", fork.ProjectPath)
	require.Equal(t, srcID, *fork.OriginSessionID)
	require.Equal(t, 3, fork.MessageCount)

	// The fork is independently retrievable.
	got, err := repo.Get(ctx, fork.ID)
	require.NoError(t, err)
	require.Equal(t, fork.ID, got.ID)
	require.Equal(t, "Original exploration (fork)", got.Title)

	// (b) The fork has copies of the 3 messages: same role and content,
	// brand-new message IDs.
	forkMsgs, err := repo.Messages(ctx, fork.ID)
	require.NoError(t, err)
	require.Len(t, forkMsgs, 3)
	for i := range srcMsgs {
		require.Equal(t, srcMsgs[i].Role, forkMsgs[i].Role)
		require.Equal(t, textOf(t, srcMsgs[i]), textOf(t, forkMsgs[i]))
		require.NotEqual(t, srcMsgs[i].ID, forkMsgs[i].ID, "copied message must get a fresh ID")
		require.NotEmpty(t, forkMsgs[i].ID)
	}
}

// TestRepo_Fork_DoesNotMutateSource verifies that appending to the fork leaves
// the source's messages and UpdatedAt unchanged.
func TestRepo_Fork_DoesNotMutateSource(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	srcID, srcMsgs := seedSource(t, ctx, repo)

	srcBefore, err := repo.Get(ctx, srcID)
	require.NoError(t, err)
	srcUpdatedBefore := srcBefore.UpdatedAt
	srcCountBefore := srcBefore.MessageCount

	// Ensure any later write would have a distinguishable timestamp.
	time.Sleep(1100 * time.Millisecond)

	fork, err := repo.Fork(ctx, srcID, session.ForkOptions{})
	require.NoError(t, err)

	// (c) Appending to the fork must not change the source.
	require.NoError(t, repo.AppendMessage(ctx, fork.ID, makeTextMsg(message.RoleAssistant, "branch-only reply")))

	forkMsgs, err := repo.Messages(ctx, fork.ID)
	require.NoError(t, err)
	require.Len(t, forkMsgs, 4, "fork should now have its own extra message")

	// Source messages unchanged in count, ids, and content.
	srcMsgsAfter, err := repo.Messages(ctx, srcID)
	require.NoError(t, err)
	require.Len(t, srcMsgsAfter, 3)
	for i := range srcMsgs {
		require.Equal(t, srcMsgs[i].ID, srcMsgsAfter[i].ID)
		require.Equal(t, textOf(t, srcMsgs[i]), textOf(t, srcMsgsAfter[i]))
	}

	// Source session metadata unchanged: UpdatedAt and MessageCount.
	srcAfter, err := repo.Get(ctx, srcID)
	require.NoError(t, err)
	require.Equal(t, srcCountBefore, srcAfter.MessageCount)
	require.True(t, srcUpdatedBefore.Equal(srcAfter.UpdatedAt),
		"source UpdatedAt must be unchanged: before=%s after=%s", srcUpdatedBefore, srcAfter.UpdatedAt)
}

// TestRepo_Fork_CutoffCopiesOnlyUpToCutoff verifies that a cutoff limits the
// copied messages to those up to and including the cutoff message.
func TestRepo_Fork_CutoffCopiesOnlyUpToCutoff(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	srcID, srcMsgs := seedSource(t, ctx, repo)

	// Cut off at the second (assistant) message, so only the first two are
	// copied.
	cutoff := srcMsgs[1].ID
	fork, err := repo.Fork(ctx, srcID, session.ForkOptions{CutoffMessageID: &cutoff})
	require.NoError(t, err)
	require.Equal(t, 2, fork.MessageCount)

	forkMsgs, err := repo.Messages(ctx, fork.ID)
	require.NoError(t, err)
	require.Len(t, forkMsgs, 2)
	require.Equal(t, textOf(t, srcMsgs[0]), textOf(t, forkMsgs[0]))
	require.Equal(t, textOf(t, srcMsgs[1]), textOf(t, forkMsgs[1]))
	require.Equal(t, message.RoleUser, forkMsgs[0].Role)
	require.Equal(t, message.RoleAssistant, forkMsgs[1].Role)

	// The third source message ("second question") must not appear in the fork.
	for _, fm := range forkMsgs {
		require.NotEqual(t, "second question", textOf(t, fm))
	}

	// Source is still intact with all three messages.
	srcMsgsAfter, err := repo.Messages(ctx, srcID)
	require.NoError(t, err)
	require.Len(t, srcMsgsAfter, 3)
}

// TestRepo_Fork_UnknownSource verifies forking a missing session returns
// ErrNotFound.
func TestRepo_Fork_UnknownSource(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	_, err := repo.Fork(ctx, "does-not-exist", session.ForkOptions{})
	require.ErrorIs(t, err, session.ErrNotFound)
}

// TestRepo_Fork_UnknownCutoff verifies a cutoff ID absent from the source
// returns ErrNotFound and creates no fork.
func TestRepo_Fork_UnknownCutoff(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	srcID, _ := seedSource(t, ctx, repo)

	bogus := "no-such-message"
	_, err := repo.Fork(ctx, srcID, session.ForkOptions{CutoffMessageID: &bogus})
	require.ErrorIs(t, err, session.ErrNotFound)
}

// TestRepo_Fork_TitleOverride verifies an explicit title is honored.
func TestRepo_Fork_TitleOverride(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	srcID, _ := seedSource(t, ctx, repo)

	fork, err := repo.Fork(ctx, srcID, session.ForkOptions{Title: "My branch"})
	require.NoError(t, err)
	require.Equal(t, "My branch", fork.Title)
}

// TestRepo_OriginOf verifies the origin link is queryable from storage and is
// nil for non-forked sessions.
func TestRepo_OriginOf(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	srcID, _ := seedSource(t, ctx, repo)

	srcOrigin, err := repo.OriginOf(ctx, srcID)
	require.NoError(t, err)
	require.Nil(t, srcOrigin, "a directly created session has no origin")

	fork, err := repo.Fork(ctx, srcID, session.ForkOptions{})
	require.NoError(t, err)

	forkOrigin, err := repo.OriginOf(ctx, fork.ID)
	require.NoError(t, err)
	require.NotNil(t, forkOrigin)
	require.Equal(t, srcID, *forkOrigin)
}
