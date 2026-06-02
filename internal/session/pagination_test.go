// Package session_test contains tests for the session module.
package session_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/session"
	"github.com/stretchr/testify/require"
)

// appendNMessages inserts n user messages into sessionID with bodies
// "msg-0", "msg-1", ... "msg-(n-1)" in that order and returns those bodies.
// The messages are inserted back-to-back, so many share the same created_at
// second; this is deliberate, exercising the paged query's stable tie-break.
func appendNMessages(ctx context.Context, t *testing.T, repo *session.Repo, sessionID string, n int) []string {
	t.Helper()
	bodies := make([]string, n)
	for i := range n {
		body := fmt.Sprintf("msg-%d", i)
		bodies[i] = body
		require.NoError(t, repo.AppendMessage(ctx, sessionID, makeTextMsg(message.RoleUser, body)))
	}
	return bodies
}

// pageBodies extracts the first text block of each message as a slice of
// strings, for comparison against expected insertion order.
func pageBodies(t *testing.T, msgs []message.Message) []string {
	t.Helper()
	out := make([]string, len(msgs))
	for i, m := range msgs {
		require.NotEmpty(t, m.Content, "message %d has no content blocks", i)
		block, ok := m.Content[0].(message.TextBlock)
		require.True(t, ok, "message %d first block is not a TextBlock", i)
		out[i] = block.Text
	}
	return out
}

// TestRepo_MessageCount_ReturnsN verifies MessageCount counts the rows
// actually stored for a session.
func TestRepo_MessageCount_ReturnsN(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	s := makeSession("count-sess", "/project/count", "Count Test")
	require.NoError(t, repo.Create(ctx, s))

	const n = 25
	appendNMessages(ctx, t, repo, "count-sess", n)

	got, err := repo.MessageCount(ctx, "count-sess")
	require.NoError(t, err)
	require.Equal(t, n, got)
}

// TestRepo_MessageCount_Empty verifies that a session with no messages, and
// an unknown session, both count as zero without error.
func TestRepo_MessageCount_Empty(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	s := makeSession("empty-count", "/project/ec", "Empty")
	require.NoError(t, repo.Create(ctx, s))

	got, err := repo.MessageCount(ctx, "empty-count")
	require.NoError(t, err)
	require.Equal(t, 0, got)

	got, err = repo.MessageCount(ctx, "does-not-exist")
	require.NoError(t, err)
	require.Equal(t, 0, got)
}

// TestRepo_MessagesPage_Windows verifies that MessagesPage returns the right
// window of messages, in oldest-first order, for several (limit, offset)
// pairs. The expected slice is derived from the known insertion order.
func TestRepo_MessagesPage_Windows(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	s := makeSession("page-sess", "/project/page", "Page Test")
	require.NoError(t, repo.Create(ctx, s))

	const n = 20
	bodies := appendNMessages(ctx, t, repo, "page-sess", n)

	cases := []struct {
		name   string
		limit  int
		offset int
		want   []string
	}{
		{"first page", 5, 0, bodies[0:5]},
		{"second page", 5, 5, bodies[5:10]},
		{"third page", 5, 10, bodies[10:15]},
		{"last full page", 5, 15, bodies[15:20]},
		{"offset 0 limit 1", 1, 0, bodies[0:1]},
		{"single in middle", 1, 7, bodies[7:8]},
		{"limit larger than remaining", 8, 16, bodies[16:20]},
		{"limit larger than total", 100, 0, bodies[0:20]},
		{"whole window via large limit and offset", 7, 13, bodies[13:20]},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := repo.MessagesPage(ctx, "page-sess", tc.limit, tc.offset)
			require.NoError(t, err)
			require.Equal(t, tc.want, pageBodies(t, got))
		})
	}
}

// TestRepo_MessagesPage_TilesToFullTranscript verifies that walking a session
// page by page reconstructs exactly the same sequence Messages returns, with
// no overlap or gap. Because all messages here are inserted within the same
// second, this is the real test that the paged query's rowid tie-break gives
// a stable total order matching Messages.
func TestRepo_MessagesPage_TilesToFullTranscript(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	s := makeSession("tile-sess", "/project/tile", "Tile Test")
	require.NoError(t, repo.Create(ctx, s))

	const n = 23
	appendNMessages(ctx, t, repo, "tile-sess", n)

	all, err := repo.Messages(ctx, "tile-sess")
	require.NoError(t, err)
	require.Len(t, all, n)
	wantAll := pageBodies(t, all)

	// Page through with a window that does not evenly divide n.
	const limit = 5
	var got []string
	for offset := 0; ; offset += limit {
		page, err := repo.MessagesPage(ctx, "tile-sess", limit, offset)
		require.NoError(t, err)
		if len(page) == 0 {
			break
		}
		require.LessOrEqual(t, len(page), limit)
		got = append(got, pageBodies(t, page)...)
	}

	require.Equal(t, wantAll, got, "paged traversal must equal Messages() order exactly")
}

// TestRepo_MessagesPage_OffsetBeyondEnd verifies that an offset past the last
// message yields an empty (non-nil) page rather than an error.
func TestRepo_MessagesPage_OffsetBeyondEnd(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	s := makeSession("beyond-sess", "/project/beyond", "Beyond")
	require.NoError(t, repo.Create(ctx, s))
	appendNMessages(ctx, t, repo, "beyond-sess", 3)

	got, err := repo.MessagesPage(ctx, "beyond-sess", 10, 100)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Empty(t, got)
}

// TestRepo_MessagesPage_NonPositiveLimit verifies that a limit of zero or less
// returns an empty, non-nil slice and never reads rows.
func TestRepo_MessagesPage_NonPositiveLimit(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	s := makeSession("zero-sess", "/project/zero", "Zero")
	require.NoError(t, repo.Create(ctx, s))
	appendNMessages(ctx, t, repo, "zero-sess", 5)

	for _, limit := range []int{0, -1, -100} {
		got, err := repo.MessagesPage(ctx, "zero-sess", limit, 0)
		require.NoError(t, err)
		require.NotNil(t, got)
		require.Empty(t, got)
	}
}

// TestRepo_MessagesPage_NegativeOffset verifies that a negative offset is
// clamped to zero, returning the first window.
func TestRepo_MessagesPage_NegativeOffset(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	s := makeSession("neg-sess", "/project/neg", "Neg")
	require.NoError(t, repo.Create(ctx, s))
	bodies := appendNMessages(ctx, t, repo, "neg-sess", 6)

	got, err := repo.MessagesPage(ctx, "neg-sess", 3, -5)
	require.NoError(t, err)
	require.Equal(t, bodies[0:3], pageBodies(t, got))
}

// TestRepo_MessagesPage_UnknownSession verifies that paging an unknown session
// returns no messages and no error (consistent with Messages).
func TestRepo_MessagesPage_UnknownSession(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	got, err := repo.MessagesPage(ctx, "nope", 10, 0)
	require.NoError(t, err)
	require.Empty(t, got)
}

// TestRepo_MessagesPage_PreservesContent verifies a paged message round-trips
// its content faithfully, not just its ordering.
func TestRepo_MessagesPage_PreservesContent(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	s := makeSession("content-sess", "/project/content", "Content")
	require.NoError(t, repo.Create(ctx, s))

	require.NoError(t, repo.AppendMessage(ctx, "content-sess", makeTextMsg(message.RoleUser, "alpha")))
	require.NoError(t, repo.AppendMessage(ctx, "content-sess", makeTextMsg(message.RoleAssistant, "beta")))

	got, err := repo.MessagesPage(ctx, "content-sess", 1, 1)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, message.RoleAssistant, got[0].Role)
	block, ok := got[0].Content[0].(message.TextBlock)
	require.True(t, ok)
	require.Equal(t, "beta", block.Text)
}
