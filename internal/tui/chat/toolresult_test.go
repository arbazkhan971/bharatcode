package chat

import (
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/stretchr/testify/require"
)

// TestReloadedToolResultRendersAsActivityTurn asserts that a persisted tool
// result — a user-role message whose sole block is a ToolResultBlock, the shape
// the agent loop stores — renders as a styled "Result" activity turn on reload,
// matching the live path rather than appearing as plain user-bubble prose.
func TestReloadedToolResultRendersAsActivityTurn(t *testing.T) {
	l := New()
	l.Append(message.Message{
		ID:   "r1",
		Role: message.RoleUser,
		Content: []message.ContentBlock{message.ToolResultBlock{
			Content: "all tests passed",
		}},
	})

	out := stripANSI(l.Render(80))
	require.Contains(t, out, "Result", "a reloaded tool result must lead with the Result verb")
	require.Contains(t, out, "all tests passed", "the result content must still render")
}

// TestUserProseNotMistakenForToolResult asserts a genuine user prompt (a text
// block) is left as a user turn and does not get relabelled as a tool result.
func TestUserProseNotMistakenForToolResult(t *testing.T) {
	l := New()
	l.Append(message.Message{
		ID:   "u1",
		Role: message.RoleUser,
		Content: []message.ContentBlock{message.TextBlock{
			Text: "please run the tests",
		}},
	})

	out := stripANSI(l.Render(80))
	require.NotContains(t, out, "Result", "user prose must not render as a Result activity turn")
	require.Contains(t, out, "please run the tests")
}

// TestIsSoleToolResult exercises the block-shape detector directly.
func TestIsSoleToolResult(t *testing.T) {
	tests := []struct {
		name string
		msg  message.Message
		want bool
	}{
		{
			name: "sole tool result",
			msg:  message.Message{Content: []message.ContentBlock{message.ToolResultBlock{Content: "x"}}},
			want: true,
		},
		{
			name: "text prose",
			msg:  message.Message{Content: []message.ContentBlock{message.TextBlock{Text: "hi"}}},
			want: false,
		},
		{
			name: "empty content",
			msg:  message.Message{},
			want: false,
		},
		{
			name: "result mixed with prose",
			msg: message.Message{Content: []message.ContentBlock{
				message.TextBlock{Text: "note"},
				message.ToolResultBlock{Content: "x"},
			}},
			want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, isSoleToolResult(tc.msg))
		})
	}
}
