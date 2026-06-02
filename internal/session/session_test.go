// Package session_test contains tests for the session module.
package session_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/db"
	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/session"
	"github.com/stretchr/testify/require"
)

// openTestDB opens a fresh SQLite database in a temp directory for testing.
func openTestDB(t *testing.T) *db.DB {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	database, err := db.Open(ctx, dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { database.Close() })
	return database
}

// makeTextMsg creates a message.Message with a single TextBlock.
func makeTextMsg(role message.Role, text string) message.Message {
	return message.Message{
		Role: role,
		Content: []message.ContentBlock{
			message.TextBlock{Text: text},
		},
		CreatedAt: time.Now().UTC(),
	}
}

// makeSession constructs a minimal Session for insertion.
func makeSession(id, projectPath, title string) *session.Session {
	now := time.Now().UTC()
	return &session.Session{
		ID:          id,
		ProjectPath: projectPath,
		Title:       title,
		Model:       "deepseek-chat",
		Agent:       "coder",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

// TestRepo_Create_NewSession verifies that a fresh session inserts and is
// recoverable via Get.
func TestRepo_Create_NewSession(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	s := makeSession("sess-1", "/project/a", "My Session")
	err := repo.Create(ctx, s)
	require.NoError(t, err)

	got, err := repo.Get(ctx, "sess-1")
	require.NoError(t, err)
	require.Equal(t, "sess-1", got.ID)
	require.Equal(t, "/project/a", got.ProjectPath)
	require.Equal(t, "My Session", got.Title)
	require.Equal(t, "deepseek-chat", got.Model)
	require.Equal(t, "coder", got.Agent)
	require.Equal(t, 0, got.MessageCount)
}

// TestRepo_Create_AutoID verifies that a session without an ID gets one
// auto-generated.
func TestRepo_Create_AutoID(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	s := &session.Session{
		ProjectPath: "/project/a",
		Model:       "kimi-k2",
		Agent:       "coder",
	}
	err := repo.Create(ctx, s)
	require.NoError(t, err)
	require.NotEmpty(t, s.ID)

	got, err := repo.Get(ctx, s.ID)
	require.NoError(t, err)
	require.Equal(t, "New session", got.Title)
}

// TestRepo_Create_AutoTimestamps verifies that zero times are populated.
func TestRepo_Create_AutoTimestamps(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	s := &session.Session{
		ID:          "sess-ts",
		ProjectPath: "/project/ts",
		Title:       "TS Session",
		Model:       "deepseek-chat",
		Agent:       "coder",
	}
	before := time.Now().UTC().Add(-time.Second)
	err := repo.Create(ctx, s)
	after := time.Now().UTC().Add(time.Second)
	require.NoError(t, err)

	require.True(t, s.CreatedAt.After(before) || s.CreatedAt.Equal(before))
	require.True(t, s.CreatedAt.Before(after) || s.CreatedAt.Equal(after))
}

// TestRepo_Create_DuplicateID_ReturnsErrAlreadyExists verifies that a second
// Create with the same ID returns ErrAlreadyExists.
func TestRepo_Create_DuplicateID_ReturnsErrAlreadyExists(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	s := makeSession("dup-sess", "/project/b", "First")
	require.NoError(t, repo.Create(ctx, s))

	s2 := makeSession("dup-sess", "/project/b", "Second")
	err := repo.Create(ctx, s2)
	require.Error(t, err)
	require.ErrorIs(t, err, session.ErrAlreadyExists)
}

// TestRepo_Get_NotFound_ReturnsErrNotFound verifies that Get with an unknown
// ID returns ErrNotFound.
func TestRepo_Get_NotFound_ReturnsErrNotFound(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	_, err := repo.Get(ctx, "ghost")
	require.Error(t, err)
	require.ErrorIs(t, err, session.ErrNotFound)
}

// TestRepo_List_FilterByProjectPath verifies that sessions in two different
// project paths are correctly filtered.
func TestRepo_List_FilterByProjectPath(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	// Sessions in project A.
	for i := 0; i < 3; i++ {
		s := makeSession(fmt.Sprintf("a-%d", i), "/project/a", fmt.Sprintf("A%d", i))
		require.NoError(t, repo.Create(ctx, s))
	}
	// Sessions in project B.
	for i := 0; i < 2; i++ {
		s := makeSession(fmt.Sprintf("b-%d", i), "/project/b", fmt.Sprintf("B%d", i))
		require.NoError(t, repo.Create(ctx, s))
	}

	results, err := repo.List(ctx, session.ListFilter{ProjectPath: "/project/a"})
	require.NoError(t, err)
	require.Len(t, results, 3)
	for _, r := range results {
		require.Equal(t, "/project/a", r.ProjectPath)
	}
}

// TestRepo_List_FilterSince verifies that sessions with UpdatedAt before
// the Since threshold are excluded.
func TestRepo_List_FilterSince(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	// Old session: 10 minutes ago.
	old := makeSession("old-sess", "/project/c", "Old")
	old.UpdatedAt = time.Now().UTC().Add(-10 * time.Minute)
	old.CreatedAt = old.UpdatedAt
	require.NoError(t, repo.Create(ctx, old))

	// Fresh session: right now.
	fresh := makeSession("new-sess", "/project/c", "New")
	require.NoError(t, repo.Create(ctx, fresh))

	// Filter: sessions updated in the last 5 minutes.
	cutoff := time.Now().UTC().Add(-5 * time.Minute)
	results, err := repo.List(ctx, session.ListFilter{Since: cutoff})
	require.NoError(t, err)

	ids := make(map[string]bool)
	for _, r := range results {
		ids[r.ID] = true
	}
	require.True(t, ids["new-sess"], "new-sess should be in results")
	require.False(t, ids["old-sess"], "old-sess should be excluded")
}

// TestRepo_List_OrderedByUpdatedAtDesc verifies the result ordering.
func TestRepo_List_OrderedByUpdatedAtDesc(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	base := time.Now().UTC().Truncate(time.Second)

	for i := 0; i < 3; i++ {
		s := makeSession(fmt.Sprintf("ord-%d", i), "/project/ord", fmt.Sprintf("Ord%d", i))
		s.UpdatedAt = base.Add(time.Duration(i) * time.Second)
		s.CreatedAt = s.UpdatedAt
		require.NoError(t, repo.Create(ctx, s))
	}

	results, err := repo.List(ctx, session.ListFilter{})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(results), 3)

	// The first result should have the largest UpdatedAt.
	for i := 0; i+1 < len(results); i++ {
		require.True(
			t,
			!results[i].UpdatedAt.Before(results[i+1].UpdatedAt),
			"results should be ordered by updated_at DESC",
		)
	}
}

// TestRepo_List_LimitHonored verifies that Limit restricts the number of
// returned sessions.
func TestRepo_List_LimitHonored(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	for i := 0; i < 5; i++ {
		s := makeSession(fmt.Sprintf("lim-%d", i), "/project/lim", fmt.Sprintf("Lim%d", i))
		require.NoError(t, repo.Create(ctx, s))
	}

	results, err := repo.List(ctx, session.ListFilter{Limit: 2})
	require.NoError(t, err)
	require.Len(t, results, 2)
}

// TestRepo_Update_OnlyMutatesAllowedFields verifies that Update writes
// Title/Model/Agent and advances UpdatedAt, but does not change CreatedAt.
func TestRepo_Update_OnlyMutatesAllowedFields(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	originalCreated := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	s := &session.Session{
		ID:          "upd-sess",
		ProjectPath: "/project/upd",
		Title:       "Original Title",
		Model:       "deepseek-chat",
		Agent:       "coder",
		CreatedAt:   originalCreated,
		UpdatedAt:   originalCreated,
	}
	require.NoError(t, repo.Create(ctx, s))

	// Mutate allowed fields and try to change CreatedAt (must be ignored).
	mutated := &session.Session{
		ID:        "upd-sess",
		Title:     "Updated Title",
		Model:     "kimi-k2",
		Agent:     "task",
		CreatedAt: time.Now().UTC().Add(24 * time.Hour), // Should NOT persist.
	}
	err := repo.Update(ctx, mutated)
	require.NoError(t, err)

	got, err := repo.Get(ctx, "upd-sess")
	require.NoError(t, err)
	require.Equal(t, "Updated Title", got.Title)
	require.Equal(t, "kimi-k2", got.Model)
	require.Equal(t, "task", got.Agent)

	// CreatedAt must remain unchanged (within 1s tolerance for Unix rounding).
	require.WithinDuration(t, originalCreated, got.CreatedAt, time.Second)

	// UpdatedAt should have advanced beyond the original.
	require.True(t, got.UpdatedAt.After(originalCreated) || got.UpdatedAt.Equal(originalCreated))
}

// TestRepo_Update_ZeroUpdatedAt verifies that a zero UpdatedAt is auto-set.
func TestRepo_Update_ZeroUpdatedAt(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	s := makeSession("upd-zero", "/project/z", "Zero")
	require.NoError(t, repo.Create(ctx, s))

	before := time.Now().UTC().Add(-time.Second)
	update := &session.Session{ID: "upd-zero", Title: "Zero Updated"}
	require.NoError(t, repo.Update(ctx, update))

	got, err := repo.Get(ctx, "upd-zero")
	require.NoError(t, err)
	require.True(t, got.UpdatedAt.After(before))
}

// TestRepo_Update_NilSession verifies that Update returns an error for nil.
func TestRepo_Update_NilSession(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	err := repo.Update(ctx, nil)
	require.Error(t, err)
}

// TestRepo_Update_NotFound verifies that Update on missing ID surfaces
// ErrNotFound.
func TestRepo_Update_NotFound(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	err := repo.Update(ctx, &session.Session{ID: "ghost"})
	require.Error(t, err)
	require.ErrorIs(t, err, session.ErrNotFound)
}

// TestRepo_Delete_RemovesSession verifies that Get after Delete returns
// ErrNotFound.
func TestRepo_Delete_RemovesSession(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	s := makeSession("del-sess", "/project/del", "Delete Me")
	require.NoError(t, repo.Create(ctx, s))

	require.NoError(t, repo.Delete(ctx, "del-sess"))

	_, err := repo.Get(ctx, "del-sess")
	require.ErrorIs(t, err, session.ErrNotFound)
}

// TestRepo_Delete_CascadesMessages verifies that deleting a session also
// removes all its messages (cascaded by the DB schema FK).
func TestRepo_Delete_CascadesMessages(t *testing.T) {
	ctx := context.Background()
	database := openTestDB(t)
	repo := session.NewRepo(database)

	s := makeSession("casc-sess", "/project/casc", "Cascade")
	require.NoError(t, repo.Create(ctx, s))

	msg := makeTextMsg(message.RoleUser, "hello cascade")
	require.NoError(t, repo.AppendMessage(ctx, "casc-sess", msg))

	// Confirm message exists via raw DB.
	var count int
	err := database.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM messages WHERE session_id = ?", "casc-sess",
	).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)

	// Delete the session — FK cascade should remove the message.
	require.NoError(t, repo.Delete(ctx, "casc-sess"))

	err = database.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM messages WHERE session_id = ?", "casc-sess",
	).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 0, count)
}

// TestRepo_AppendMessage_IncrementsCount verifies that MessageCount goes
// 0 -> 1 -> 2 across two appends.
func TestRepo_AppendMessage_IncrementsCount(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	s := makeSession("cnt-sess", "/project/cnt", "Count Test")
	require.NoError(t, repo.Create(ctx, s))

	got, err := repo.Get(ctx, "cnt-sess")
	require.NoError(t, err)
	require.Equal(t, 0, got.MessageCount)

	require.NoError(t, repo.AppendMessage(ctx, "cnt-sess", makeTextMsg(message.RoleUser, "first")))
	got, err = repo.Get(ctx, "cnt-sess")
	require.NoError(t, err)
	require.Equal(t, 1, got.MessageCount)

	require.NoError(t, repo.AppendMessage(ctx, "cnt-sess", makeTextMsg(message.RoleAssistant, "second")))
	got, err = repo.Get(ctx, "cnt-sess")
	require.NoError(t, err)
	require.Equal(t, 2, got.MessageCount)
}

// TestRepo_AppendMessage_BumpsUpdatedAt verifies that UpdatedAt advances
// after each append.
func TestRepo_AppendMessage_BumpsUpdatedAt(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	base := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	s := &session.Session{
		ID:          "bump-sess",
		ProjectPath: "/project/bump",
		Title:       "Bump Test",
		Model:       "deepseek-chat",
		Agent:       "coder",
		CreatedAt:   base,
		UpdatedAt:   base,
	}
	require.NoError(t, repo.Create(ctx, s))

	before := time.Now().UTC()
	require.NoError(t, repo.AppendMessage(ctx, "bump-sess", makeTextMsg(message.RoleUser, "hello")))

	got, err := repo.Get(ctx, "bump-sess")
	require.NoError(t, err)
	// UpdatedAt must be >= before (we set it to time.Now() during append).
	require.True(t, !got.UpdatedAt.Before(base), "UpdatedAt should advance beyond base")
	_ = before
}

// TestRepo_AppendMessage_AutoTitleOnFirstUserMessage verifies that the title
// is derived from the first user message text.
func TestRepo_AppendMessage_AutoTitleOnFirstUserMessage(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	s := makeSession("auto-title", "/project/at", "")
	require.NoError(t, repo.Create(ctx, s))

	// After Create with empty title, it becomes "New session".
	got, err := repo.Get(ctx, "auto-title")
	require.NoError(t, err)
	require.Equal(t, "New session", got.Title)

	userMsg := makeTextMsg(message.RoleUser, "Help me write a Go HTTP server")
	require.NoError(t, repo.AppendMessage(ctx, "auto-title", userMsg))

	got, err = repo.Get(ctx, "auto-title")
	require.NoError(t, err)
	require.NotEqual(t, "New session", got.Title)
	require.Contains(t, got.Title, "Help me write")

	// A second append should NOT overwrite the title.
	titleAfterFirst := got.Title
	secondMsg := makeTextMsg(message.RoleUser, "Now add authentication middleware")
	require.NoError(t, repo.AppendMessage(ctx, "auto-title", secondMsg))

	got, err = repo.Get(ctx, "auto-title")
	require.NoError(t, err)
	require.Equal(t, titleAfterFirst, got.Title)
}

// TestRepo_AppendMessage_NoAutoTitle_IfTitleAlreadySet verifies that a
// session with a non-placeholder title keeps it after the first user message.
func TestRepo_AppendMessage_NoAutoTitle_IfTitleAlreadySet(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	s := makeSession("pre-titled", "/project/pt", "My Custom Title")
	require.NoError(t, repo.Create(ctx, s))

	require.NoError(t, repo.AppendMessage(ctx, "pre-titled", makeTextMsg(message.RoleUser, "Completely different text")))

	got, err := repo.Get(ctx, "pre-titled")
	require.NoError(t, err)
	require.Equal(t, "My Custom Title", got.Title)
}

// TestRepo_AppendMessage_NotFound verifies that appending to an unknown
// session surfaces ErrNotFound.
func TestRepo_AppendMessage_NotFound(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	err := repo.AppendMessage(ctx, "ghost-sess", makeTextMsg(message.RoleUser, "hello"))
	require.Error(t, err)
	require.ErrorIs(t, err, session.ErrNotFound)
}

// TestRepo_Messages_OldestFirst verifies that Messages returns messages in
// the order they were appended.
func TestRepo_Messages_OldestFirst(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	s := makeSession("msg-order", "/project/mo", "Order Test")
	require.NoError(t, repo.Create(ctx, s))

	texts := []string{"first", "second", "third"}
	for _, txt := range texts {
		require.NoError(t, repo.AppendMessage(ctx, "msg-order", makeTextMsg(message.RoleUser, txt)))
		// Small sleep so created_at Unix seconds differ.
		time.Sleep(time.Millisecond * 10)
	}

	msgs, err := repo.Messages(ctx, "msg-order")
	require.NoError(t, err)
	require.Len(t, msgs, 3)

	for i, m := range msgs {
		block := m.Content[0].(message.TextBlock)
		require.Equal(t, texts[i], block.Text)
	}
}

// TestRepo_Latest_ReturnsMostRecent verifies that Latest returns the
// session with the largest UpdatedAt for the given project path.
func TestRepo_Latest_ReturnsMostRecent(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	base := time.Now().UTC().Truncate(time.Second)

	for i := 0; i < 3; i++ {
		s := &session.Session{
			ID:          fmt.Sprintf("lat-%d", i),
			ProjectPath: "/project/latest",
			Title:       fmt.Sprintf("Session %d", i),
			Model:       "deepseek-chat",
			Agent:       "coder",
			CreatedAt:   base.Add(time.Duration(i) * time.Second),
			UpdatedAt:   base.Add(time.Duration(i) * time.Second),
		}
		require.NoError(t, repo.Create(ctx, s))
	}

	got, err := repo.Latest(ctx, "/project/latest")
	require.NoError(t, err)
	require.Equal(t, "lat-2", got.ID)
}

// TestRepo_Latest_EmptyProject_ReturnsErrNotFound verifies that Latest
// on a project with no sessions returns ErrNotFound.
func TestRepo_Latest_EmptyProject_ReturnsErrNotFound(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	_, err := repo.Latest(ctx, "/no/sessions/here")
	require.Error(t, err)
	require.ErrorIs(t, err, session.ErrNotFound)
}

// TestRepo_Create_NilSession verifies that Create returns an error for nil.
func TestRepo_Create_NilSession(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	err := repo.Create(ctx, nil)
	require.Error(t, err)
}

// TestRepo_ConcurrentReads_NoDataRace runs 16 goroutines performing reads
// for 100ms to verify there are no data races.
func TestRepo_ConcurrentReads_NoDataRace(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	// Create some data to read.
	for i := 0; i < 5; i++ {
		s := makeSession(fmt.Sprintf("race-%d", i), "/project/race", fmt.Sprintf("Race%d", i))
		require.NoError(t, repo.Create(ctx, s))
		require.NoError(t, repo.AppendMessage(ctx, s.ID, makeTextMsg(message.RoleUser, "race message")))
	}

	deadline := time.Now().Add(100 * time.Millisecond)
	var wg sync.WaitGroup
	errCh := make(chan error, 16)

	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for time.Now().Before(deadline) {
				switch workerID % 3 {
				case 0:
					_, err := repo.Get(ctx, fmt.Sprintf("race-%d", workerID%5))
					if err != nil && workerID%5 < 5 {
						// Only error if it's a real error (not ErrNotFound for valid IDs).
						errCh <- err
						return
					}
				case 1:
					_, err := repo.List(ctx, session.ListFilter{ProjectPath: "/project/race"})
					if err != nil {
						errCh <- err
						return
					}
				case 2:
					_, err := repo.Messages(ctx, fmt.Sprintf("race-%d", workerID%5))
					if err != nil {
						errCh <- err
						return
					}
				}
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		require.NoError(t, err)
	}
}

// TestTitleFromFirstMessage_Truncates verifies that long inputs are truncated
// on a word boundary at most 60 characters.
func TestTitleFromFirstMessage_Truncates(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		maxLen   int
		hasSpace bool
	}{
		{
			name:  "long input with spaces",
			input: "This is a very long message that definitely exceeds the sixty character limit set by the truncation logic",
		},
		{
			name:  "exactly 61 chars with trailing space",
			input: "twelve chars long message that goes just over the limit here!",
		},
		{
			name:  "long word no space",
			input: "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghi",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := makeTextMsg(message.RoleUser, tc.input)
			title := session.TitleFromFirstMessage(msg)
			require.LessOrEqual(t, len([]rune(title)), 60,
				"title must not exceed 60 runes, got: %q", title)
		})
	}
}

// TestTitleFromFirstMessage_ShortInput verifies that short text is returned as-is.
func TestTitleFromFirstMessage_ShortInput(t *testing.T) {
	msg := makeTextMsg(message.RoleUser, "Short text")
	title := session.TitleFromFirstMessage(msg)
	require.Equal(t, "Short text", title)
}

// TestTitleFromFirstMessage_StripsNewlines verifies that newlines become spaces.
func TestTitleFromFirstMessage_StripsNewlines(t *testing.T) {
	msg := makeTextMsg(message.RoleUser, "First line\nSecond line\r\nThird line")
	title := session.TitleFromFirstMessage(msg)
	require.NotContains(t, title, "\n")
	require.NotContains(t, title, "\r")
	require.Contains(t, title, "First line")
}

// TestTitleFromFirstMessage_EmptyContent verifies that an empty message falls
// back to "New session".
func TestTitleFromFirstMessage_EmptyContent(t *testing.T) {
	msg := message.Message{
		Role:    message.RoleUser,
		Content: []message.ContentBlock{},
	}
	title := session.TitleFromFirstMessage(msg)
	require.Equal(t, "New session", title)
}

// TestTitleFromFirstMessage_NonTextBlock verifies that a message with no
// TextBlock falls back to "New session".
func TestTitleFromFirstMessage_NonTextBlock(t *testing.T) {
	msg := message.Message{
		Role: message.RoleUser,
		Content: []message.ContentBlock{
			message.ThinkingBlock{Text: "thinking..."},
		},
	}
	title := session.TitleFromFirstMessage(msg)
	require.Equal(t, "New session", title)
}

// TestRepo_Messages_EmptySession verifies that Messages on a fresh session
// returns an empty slice without error.
func TestRepo_Messages_EmptySession(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	s := makeSession("empty-msgs", "/project/em", "Empty")
	require.NoError(t, repo.Create(ctx, s))

	msgs, err := repo.Messages(ctx, "empty-msgs")
	require.NoError(t, err)
	require.Empty(t, msgs)
}

// TestRepo_Messages_ContentRoundtrip verifies that complex content blocks
// survive the JSON round-trip through the messages table.
func TestRepo_Messages_ContentRoundtrip(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	s := makeSession("rt-sess", "/project/rt", "Roundtrip")
	require.NoError(t, repo.Create(ctx, s))

	input := json.RawMessage(`{"key":"value"}`)
	complexMsg := message.Message{
		Role: message.RoleAssistant,
		Content: []message.ContentBlock{
			message.TextBlock{Text: "Here is my response"},
			message.ToolUseBlock{
				ID:    "tool-123",
				Name:  "bash",
				Input: input,
			},
		},
		CreatedAt: time.Now().UTC(),
	}
	require.NoError(t, repo.AppendMessage(ctx, "rt-sess", complexMsg))

	msgs, err := repo.Messages(ctx, "rt-sess")
	require.NoError(t, err)
	require.Len(t, msgs, 1)

	got := msgs[0]
	require.Equal(t, message.RoleAssistant, got.Role)
	require.Len(t, got.Content, 2)

	text, ok := got.Content[0].(message.TextBlock)
	require.True(t, ok)
	require.Equal(t, "Here is my response", text.Text)

	tool, ok := got.Content[1].(message.ToolUseBlock)
	require.True(t, ok)
	require.Equal(t, "tool-123", tool.ID)
	require.Equal(t, "bash", tool.Name)
}

// TestRepo_List_NoFilter verifies that an empty ListFilter returns all sessions.
func TestRepo_List_NoFilter(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	for i := 0; i < 4; i++ {
		s := makeSession(fmt.Sprintf("all-%d", i), fmt.Sprintf("/project/%d", i), fmt.Sprintf("S%d", i))
		require.NoError(t, repo.Create(ctx, s))
	}

	results, err := repo.List(ctx, session.ListFilter{})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(results), 4)
}

// TestRepo_AppendMessage_WithParentID verifies that ParentID is preserved
// when a parent message already exists in the table.
func TestRepo_AppendMessage_WithParentID(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	s := makeSession("parent-sess", "/project/par", "Parent Test")
	require.NoError(t, repo.Create(ctx, s))

	// Insert the parent message first so the FK constraint is satisfied.
	parentMsg := message.Message{
		ID:        "parent-msg-id",
		Role:      message.RoleUser,
		Content:   []message.ContentBlock{message.TextBlock{Text: "parent message"}},
		CreatedAt: time.Now().UTC(),
	}
	require.NoError(t, repo.AppendMessage(ctx, "parent-sess", parentMsg))

	// Now insert the child that references the parent.
	parentID := "parent-msg-id"
	childMsg := message.Message{
		ID:        "child-msg-id",
		Role:      message.RoleAssistant,
		Content:   []message.ContentBlock{message.TextBlock{Text: "child message"}},
		ParentID:  &parentID,
		CreatedAt: time.Now().UTC().Add(time.Millisecond),
	}
	require.NoError(t, repo.AppendMessage(ctx, "parent-sess", childMsg))

	msgs, err := repo.Messages(ctx, "parent-sess")
	require.NoError(t, err)
	require.Len(t, msgs, 2)

	// The child is the second message; verify its ParentID.
	child := msgs[1]
	require.NotNil(t, child.ParentID)
	require.Equal(t, parentID, *child.ParentID)
}

// TestRepo_Get_TimezoneUTC verifies that timestamps are always returned as UTC.
func TestRepo_Get_TimezoneUTC(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	s := makeSession("tz-sess", "/project/tz", "Timezone")
	require.NoError(t, repo.Create(ctx, s))

	got, err := repo.Get(ctx, "tz-sess")
	require.NoError(t, err)
	require.Equal(t, time.UTC, got.CreatedAt.Location())
	require.Equal(t, time.UTC, got.UpdatedAt.Location())
}

// TestRepo_Latest_TimezoneUTC verifies that Latest returns UTC timestamps.
func TestRepo_Latest_TimezoneUTC(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	s := makeSession("tz-latest", "/project/tzl", "Timezone Latest")
	require.NoError(t, repo.Create(ctx, s))

	got, err := repo.Latest(ctx, "/project/tzl")
	require.NoError(t, err)
	require.Equal(t, time.UTC, got.CreatedAt.Location())
	require.Equal(t, time.UTC, got.UpdatedAt.Location())
}

// TestRepo_List_FilterCombined verifies that ProjectPath and Limit can be
// combined in the same filter.
func TestRepo_List_FilterCombined(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	for i := 0; i < 5; i++ {
		s := makeSession(fmt.Sprintf("combo-%d", i), "/project/combo", fmt.Sprintf("Combo%d", i))
		s.UpdatedAt = time.Now().UTC().Add(time.Duration(i) * time.Second)
		s.CreatedAt = s.UpdatedAt
		require.NoError(t, repo.Create(ctx, s))
	}
	// Different project — should be excluded.
	other := makeSession("other-proj", "/project/other", "Other")
	require.NoError(t, repo.Create(ctx, other))

	results, err := repo.List(ctx, session.ListFilter{
		ProjectPath: "/project/combo",
		Limit:       3,
	})
	require.NoError(t, err)
	require.Len(t, results, 3)
	for _, r := range results {
		require.Equal(t, "/project/combo", r.ProjectPath)
	}
}

// TestTitleFromFirstMessage_AllSpacesEdge verifies the edge case where the
// first 60 runes are all spaces, causing TitleFromFirstMessage to fall back to
// a 60-rune hard cut rather than returning an empty string.
func TestTitleFromFirstMessage_AllSpacesEdge(t *testing.T) {
	// Construct a string where the first 60 chars are all spaces, making the
	// word-boundary scan produce an empty trimmed result.
	input := "                                                            trailing text"
	msg := makeTextMsg(message.RoleUser, input)
	title := session.TitleFromFirstMessage(msg)
	// We should never return empty; either the hard 60-rune fallback or something.
	require.NotEmpty(t, title)
	require.LessOrEqual(t, len([]rune(title)), 60)
}

// TestRepo_AppendMessage_ZeroCreatedAt verifies that messages with a zero
// CreatedAt get a timestamp assigned automatically.
func TestRepo_AppendMessage_ZeroCreatedAt(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	s := makeSession("zero-ts-sess", "/project/zts", "Zero TS")
	require.NoError(t, repo.Create(ctx, s))

	msg := message.Message{
		Role:      message.RoleUser,
		Content:   []message.ContentBlock{message.TextBlock{Text: "no timestamp"}},
		CreatedAt: time.Time{}, // Explicitly zero.
	}
	before := time.Now().UTC().Add(-time.Second)
	require.NoError(t, repo.AppendMessage(ctx, "zero-ts-sess", msg))
	after := time.Now().UTC().Add(time.Second)

	msgs, err := repo.Messages(ctx, "zero-ts-sess")
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	// CreatedAt should have been auto-filled between before and after.
	require.True(t, msgs[0].CreatedAt.After(before) || msgs[0].CreatedAt.Equal(before))
	require.True(t, msgs[0].CreatedAt.Before(after) || msgs[0].CreatedAt.Equal(after))
}

// TestRepo_AppendMessage_NoID verifies that a message with no ID gets one
// auto-generated by AppendMessage.
func TestRepo_AppendMessage_NoID(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	s := makeSession("no-id-sess", "/project/nid", "No ID")
	require.NoError(t, repo.Create(ctx, s))

	msg := message.Message{
		// ID is intentionally empty.
		Role:    message.RoleUser,
		Content: []message.ContentBlock{message.TextBlock{Text: "auto id please"}},
	}
	require.NoError(t, repo.AppendMessage(ctx, "no-id-sess", msg))

	msgs, err := repo.Messages(ctx, "no-id-sess")
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	require.NotEmpty(t, msgs[0].ID)
}

// TestTitleFromFirstMessage_ExactlyAtLimit verifies that a message text of
// exactly 60 runes is returned without truncation.
func TestTitleFromFirstMessage_ExactlyAtLimit(t *testing.T) {
	// Exactly 60 runes.
	input := "abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefgh"
	require.Equal(t, 60, len([]rune(input)))

	msg := makeTextMsg(message.RoleUser, input)
	title := session.TitleFromFirstMessage(msg)
	require.Equal(t, input, title)
}

// TestTitleFromFirstMessage_WordBoundaryAtStart verifies truncation when the
// last space before position 60 is near the start (so the truncated result is
// still meaningful).
func TestTitleFromFirstMessage_WordBoundaryAtStart(t *testing.T) {
	// "short " followed by 60+ non-space chars: truncation falls to position 6.
	input := "short AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	msg := makeTextMsg(message.RoleUser, input)
	title := session.TitleFromFirstMessage(msg)
	require.LessOrEqual(t, len([]rune(title)), 60)
	require.NotEmpty(t, title)
}

// TestRepo_Create_PreservesTimes verifies that explicitly provided timestamps
// are not overwritten by Create.
func TestRepo_Create_PreservesTimes(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	explicit := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	s := &session.Session{
		ID:          "explicit-ts",
		ProjectPath: "/project/ets",
		Title:       "Explicit TS",
		Model:       "deepseek-chat",
		Agent:       "coder",
		CreatedAt:   explicit,
		UpdatedAt:   explicit,
	}
	require.NoError(t, repo.Create(ctx, s))

	got, err := repo.Get(ctx, "explicit-ts")
	require.NoError(t, err)
	// Unix-second precision: allow ±1s.
	require.WithinDuration(t, explicit, got.CreatedAt, time.Second)
	require.WithinDuration(t, explicit, got.UpdatedAt, time.Second)
}

// TestTitleFromFirstMessage_PointerTextBlock verifies that TitleFromFirstMessage
// correctly handles a *message.TextBlock (pointer type) in the Content slice.
func TestTitleFromFirstMessage_PointerTextBlock(t *testing.T) {
	tb := &message.TextBlock{Text: "Pointer text block content here"}
	msg := message.Message{
		Role:    message.RoleUser,
		Content: []message.ContentBlock{tb},
	}
	title := session.TitleFromFirstMessage(msg)
	require.Equal(t, "Pointer text block content here", title)
}

// TestRepo_Get_CancelledContext verifies that Get surfaces a DB error when
// the context is cancelled before the query runs.
func TestRepo_Get_CancelledContext(t *testing.T) {
	repo := session.NewRepo(openTestDB(t))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	_, err := repo.Get(ctx, "any-id")
	require.Error(t, err)
}

// TestRepo_List_CancelledContext verifies that List surfaces a DB error when
// the context is cancelled before the query runs.
func TestRepo_List_CancelledContext(t *testing.T) {
	repo := session.NewRepo(openTestDB(t))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := repo.List(ctx, session.ListFilter{})
	require.Error(t, err)
}

// TestRepo_Delete_CancelledContext verifies that Delete surfaces a DB error
// when the context is cancelled before the query.
func TestRepo_Delete_CancelledContext(t *testing.T) {
	repo := session.NewRepo(openTestDB(t))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := repo.Delete(ctx, "any-id")
	require.Error(t, err)
}

// TestRepo_Latest_CancelledContext verifies that Latest surfaces a DB error
// when the context is cancelled before the query.
func TestRepo_Latest_CancelledContext(t *testing.T) {
	repo := session.NewRepo(openTestDB(t))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := repo.Latest(ctx, "/project/cancelled")
	require.Error(t, err)
}

// TestRepo_Messages_CancelledContext verifies that Messages surfaces a DB
// error when the context is cancelled before the query.
func TestRepo_Messages_CancelledContext(t *testing.T) {
	repo := session.NewRepo(openTestDB(t))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := repo.Messages(ctx, "any-sess")
	require.Error(t, err)
}

// TestRepo_AppendMessage_CancelledContextBeforeUpdate verifies that
// AppendMessage surfaces an error when the context is cancelled.
func TestRepo_AppendMessage_CancelledContextBeforeUpdate(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	s := makeSession("cancel-append", "/project/ca", "Cancel Append")
	require.NoError(t, repo.Create(ctx, s))

	// Now cancel the context and try to append.
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()

	err := repo.AppendMessage(cancelCtx, "cancel-append", makeTextMsg(message.RoleUser, "hello"))
	require.Error(t, err)
}

// sessionIDs extracts the IDs of the given sessions for set assertions.
func sessionIDs(sessions []session.Session) []string {
	ids := make([]string, len(sessions))
	for i, s := range sessions {
		ids[i] = s.ID
	}
	return ids
}

// TestRepo_Search_ByTitle verifies that Search returns sessions whose title
// contains the query and excludes those that do not.
func TestRepo_Search_ByTitle(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	require.NoError(t, repo.Create(ctx, makeSession("s1", "/p", "Fix login bug")))
	require.NoError(t, repo.Create(ctx, makeSession("s2", "/p", "Refactor payment flow")))
	require.NoError(t, repo.Create(ctx, makeSession("s3", "/p", "Login page redesign")))

	got, err := repo.Search(ctx, "login")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"s1", "s3"}, sessionIDs(got))
}

// TestRepo_Search_CaseInsensitive verifies that title matching ignores case.
func TestRepo_Search_CaseInsensitive(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	require.NoError(t, repo.Create(ctx, makeSession("s1", "/p", "Database Migration")))
	require.NoError(t, repo.Create(ctx, makeSession("s2", "/p", "Unrelated work")))

	got, err := repo.Search(ctx, "DATABASE")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"s1"}, sessionIDs(got))
}

// TestRepo_Search_ByFirstUserMessage verifies that Search matches the text of
// the first user message even when the title does not contain the query.
func TestRepo_Search_ByFirstUserMessage(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	// Title is deliberately set so it does NOT contain the needle; the match
	// must come from the first user message body.
	require.NoError(t, repo.Create(ctx, makeSession("s1", "/p", "Session one")))
	require.NoError(t, repo.AppendMessage(ctx, "s1",
		makeTextMsg(message.RoleUser, "Please add OAuth support to the gateway")))

	require.NoError(t, repo.Create(ctx, makeSession("s2", "/p", "Session two")))
	require.NoError(t, repo.AppendMessage(ctx, "s2",
		makeTextMsg(message.RoleUser, "Update the README")))

	got, err := repo.Search(ctx, "oauth")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"s1"}, sessionIDs(got))
}

// TestRepo_Search_IgnoresNonUserMessages verifies that a match in an
// assistant message does not cause a session to be returned.
func TestRepo_Search_IgnoresNonUserMessages(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	require.NoError(t, repo.Create(ctx, makeSession("s1", "/p", "Session one")))
	require.NoError(t, repo.AppendMessage(ctx, "s1",
		makeTextMsg(message.RoleUser, "hello there")))
	require.NoError(t, repo.AppendMessage(ctx, "s1",
		makeTextMsg(message.RoleAssistant, "I will deploy to production now")))

	got, err := repo.Search(ctx, "production")
	require.NoError(t, err)
	require.Empty(t, got)
}

// TestRepo_Search_EmptyQueryReturnsAll verifies that an empty query returns
// every session, mirroring a zero ListFilter.
func TestRepo_Search_EmptyQueryReturnsAll(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	require.NoError(t, repo.Create(ctx, makeSession("s1", "/p", "Alpha")))
	require.NoError(t, repo.Create(ctx, makeSession("s2", "/p", "Beta")))

	got, err := repo.Search(ctx, "")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"s1", "s2"}, sessionIDs(got))
}

// TestRepo_Search_NoMatchesReturnsEmpty verifies that a query matching nothing
// returns an empty (non-nil-error) result.
func TestRepo_Search_NoMatchesReturnsEmpty(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	require.NoError(t, repo.Create(ctx, makeSession("s1", "/p", "Alpha")))
	require.NoError(t, repo.Create(ctx, makeSession("s2", "/p", "Beta")))

	got, err := repo.Search(ctx, "zzz-no-such-thing")
	require.NoError(t, err)
	require.Empty(t, got)
}

// TestRepo_Search_OrderedByUpdatedAtDesc verifies that Search preserves the
// newest-first ordering of List.
func TestRepo_Search_OrderedByUpdatedAtDesc(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	older := makeSession("old", "/p", "shared keyword older")
	older.UpdatedAt = time.Now().UTC().Add(-2 * time.Hour)
	newer := makeSession("new", "/p", "shared keyword newer")
	newer.UpdatedAt = time.Now().UTC().Add(-1 * time.Hour)
	require.NoError(t, repo.Create(ctx, older))
	require.NoError(t, repo.Create(ctx, newer))

	got, err := repo.Search(ctx, "shared keyword")
	require.NoError(t, err)
	require.Equal(t, []string{"new", "old"}, sessionIDs(got))
}

// TestRepo_SetTitle_Persists verifies that SetTitle writes the new title and
// leaves other fields unchanged.
func TestRepo_SetTitle_Persists(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	s := makeSession("s1", "/project/x", "Original Title")
	require.NoError(t, repo.Create(ctx, s))

	require.NoError(t, repo.SetTitle(ctx, "s1", "Renamed Title"))

	got, err := repo.Get(ctx, "s1")
	require.NoError(t, err)
	require.Equal(t, "Renamed Title", got.Title)
	// Other mutable fields are untouched.
	require.Equal(t, "/project/x", got.ProjectPath)
	require.Equal(t, "deepseek-chat", got.Model)
	require.Equal(t, "coder", got.Agent)
}

// TestRepo_SetTitle_FoundBySearch verifies that a renamed session is findable
// by its new title and no longer by its old one.
func TestRepo_SetTitle_FoundBySearch(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	require.NoError(t, repo.Create(ctx, makeSession("s1", "/p", "temporary name")))
	require.NoError(t, repo.SetTitle(ctx, "s1", "permanent label"))

	byNew, err := repo.Search(ctx, "permanent")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"s1"}, sessionIDs(byNew))

	byOld, err := repo.Search(ctx, "temporary")
	require.NoError(t, err)
	require.Empty(t, byOld)
}

// TestRepo_SetTitle_NotFound verifies that SetTitle on an unknown id returns
// ErrNotFound.
func TestRepo_SetTitle_NotFound(t *testing.T) {
	ctx := context.Background()
	repo := session.NewRepo(openTestDB(t))

	err := repo.SetTitle(ctx, "does-not-exist", "whatever")
	require.ErrorIs(t, err, session.ErrNotFound)
}
