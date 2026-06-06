package chat

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/stretchr/testify/require"
)

// TestTimestamp_ShownInHeader checks that a message with a non-zero CreatedAt
// shows "HH:MM" on the same line as the role prefix.
func TestTimestamp_ShownInHeader(t *testing.T) {
	t.Parallel()

	ts := time.Now().Truncate(time.Minute)
	list := New()
	list.Append(message.Message{
		ID:        "u1",
		Role:      message.RoleUser,
		Content:   []message.ContentBlock{message.TextBlock{Text: "hello"}},
		CreatedAt: ts,
	})
	out := list.Render(80)
	want := "user · " + ts.Format("15:04")
	require.True(t, strings.Contains(out, want), "render should contain %q, got:\n%s", want, out)
}

// TestTimestamp_AbsentWhenZero checks that a message with zero CreatedAt omits
// the "· HH:MM" segment so old or synthesised messages stay clean.
func TestTimestamp_AbsentWhenZero(t *testing.T) {
	t.Parallel()

	list := New()
	list.Append(message.Message{
		ID:      "u1",
		Role:    message.RoleUser,
		Content: []message.ContentBlock{message.TextBlock{Text: "hello"}},
		// CreatedAt intentionally left zero
	})
	out := list.Render(80)
	require.False(t, strings.Contains(out, "·"), "zero-time message must not contain separator, got:\n%s", out)
}

// TestTimestamp_OlderDayShowsDate checks that a message from a different
// calendar day is formatted as "Jan 2 15:04" rather than just "15:04".
func TestTimestamp_OlderDayShowsDate(t *testing.T) {
	t.Parallel()

	yesterday := time.Now().AddDate(0, 0, -1).Truncate(time.Minute)
	list := New()
	list.Append(message.Message{
		ID:        "a1",
		Role:      message.RoleAssistant,
		Content:   []message.ContentBlock{message.TextBlock{Text: "reply"}},
		CreatedAt: yesterday,
	})
	out := list.Render(80)
	want := yesterday.Format("Jan 2 15:04")
	require.True(t, strings.Contains(out, want), "older message should contain date %q, got:\n%s", want, out)
}

// TestTimestamp_StreamingHasNoTimestamp verifies that streamed messages (which
// have no server-assigned CreatedAt) omit the timestamp segment.
func TestTimestamp_StreamingHasNoTimestamp(t *testing.T) {
	t.Parallel()

	list := New()
	list.Stream("s1", "streaming content")
	out := list.Render(80)
	require.False(t, strings.Contains(out, "·"), "streaming message must not have timestamp, got:\n%s", out)
}

// TestTimestamp_ReappendPreservesTimestamp verifies that re-appending an
// existing message ID with a zero CreatedAt does not overwrite the original
// timestamp (guards against a cache-invalidation re-append clearing the time).
func TestTimestamp_ReappendPreservesTimestamp(t *testing.T) {
	t.Parallel()

	ts := time.Now().Truncate(time.Minute)
	list := New()
	list.Append(message.Message{
		ID:        "u1",
		Role:      message.RoleUser,
		Content:   []message.ContentBlock{message.TextBlock{Text: "first"}},
		CreatedAt: ts,
	})
	// Re-append same ID with no timestamp (simulates a delta update).
	list.Append(message.Message{
		ID:      "u1",
		Role:    message.RoleUser,
		Content: []message.ContentBlock{message.TextBlock{Text: "updated"}},
	})
	out := list.Render(80)
	want := "user · " + ts.Format("15:04")
	require.True(t, strings.Contains(out, want), "re-append must preserve original timestamp, got:\n%s", out)
}

func TestStreamRender_NoFlicker(t *testing.T) {
	t.Parallel()

	list := New()
	seen := make(map[string]struct{})
	for i := 0; i < 100; i++ {
		list.Stream("assistant-1", fmt.Sprintf("%02d ", i))
		seen[list.Render(80)] = struct{}{}
	}
	list.FinishStream("assistant-1")
	seen[list.Render(80)] = struct{}{}

	require.LessOrEqual(t, len(seen), 101)
	require.LessOrEqual(t, list.RenderRegions(), 101)
}
