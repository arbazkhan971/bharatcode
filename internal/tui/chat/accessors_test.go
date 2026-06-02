package chat

import (
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/stretchr/testify/require"
)

func msg(id string, role message.Role, text string) message.Message {
	return message.Message{
		ID:      id,
		Role:    role,
		Content: []message.ContentBlock{message.TextBlock{Text: text}},
	}
}

// TestLastAssistantText returns the most recent assistant body verbatim and is
// empty when no assistant message is present.
func TestLastAssistantText(t *testing.T) {
	t.Parallel()

	list := New()
	require.Empty(t, list.LastAssistantText(), "empty list has no assistant text")

	list.Append(msg("u1", message.RoleUser, "question one"))
	require.Empty(t, list.LastAssistantText(), "a user-only list has no assistant text")

	list.Append(msg("a1", message.RoleAssistant, "first answer"))
	list.Append(msg("u2", message.RoleUser, "question two"))
	list.Append(msg("a2", message.RoleAssistant, "second answer"))

	require.Equal(t, "second answer", list.LastAssistantText(),
		"must return the most recent assistant body, not an earlier one or a user message")
}

// TestTranscriptText renders every message as role-prefixed plain text in order.
func TestTranscriptText(t *testing.T) {
	t.Parallel()

	list := New()
	require.Empty(t, list.TranscriptText(), "empty list has no transcript")

	list.Append(msg("u1", message.RoleUser, "fix the bug"))
	list.Append(msg("a1", message.RoleAssistant, "bug fixed"))

	got := list.TranscriptText()
	require.Contains(t, got, "user:\nfix the bug")
	require.Contains(t, got, "assistant:\nbug fixed")
	// User turn must precede the assistant turn.
	require.Less(t, indexOf(got, "fix the bug"), indexOf(got, "bug fixed"),
		"transcript must preserve message order")
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
