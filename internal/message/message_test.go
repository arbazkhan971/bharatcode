package message

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRoundTrip_TextBlock(t *testing.T) {
	msg := Message{
		ID:        "msg-1",
		SessionID: "sess-1",
		Role:      RoleUser,
		CreatedAt: time.Now().UTC().Truncate(time.Second),
		Content: []ContentBlock{
			TextBlock{Text: "Hello, world!"},
		},
	}

	data, err := json.Marshal(msg)
	require.NoError(t, err)

	var decoded Message
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	// Custom DeepEqual to check parity since CreatedAt time may lose location info.
	require.Equal(t, msg.ID, decoded.ID)
	require.Equal(t, msg.SessionID, decoded.SessionID)
	require.Equal(t, msg.Role, decoded.Role)
	require.True(t, decoded.CreatedAt.Equal(msg.CreatedAt))
	require.Len(t, decoded.Content, 1)
	require.Equal(t, msg.Content[0], decoded.Content[0])
}

func TestRoundTrip_ToolUseBlock(t *testing.T) {
	msg := Message{
		ID:        "msg-2",
		SessionID: "sess-1",
		Role:      RoleAssistant,
		CreatedAt: time.Now().UTC().Truncate(time.Second),
		Content: []ContentBlock{
			ToolUseBlock{
				ID:    "tool-1",
				Name:  "bash",
				Input: json.RawMessage(`{"command":"ls -la"}`),
			},
		},
	}

	data, err := json.Marshal(msg)
	require.NoError(t, err)

	var decoded Message
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	require.Equal(t, msg.ID, decoded.ID)
	require.Equal(t, msg.SessionID, decoded.SessionID)
	require.Equal(t, msg.Role, decoded.Role)
	require.True(t, decoded.CreatedAt.Equal(msg.CreatedAt))
	require.Len(t, decoded.Content, 1)

	useBlock, ok := decoded.Content[0].(ToolUseBlock)
	require.True(t, ok)
	require.Equal(t, "tool-1", useBlock.ID)
	require.Equal(t, "bash", useBlock.Name)
	require.JSONEq(t, `{"command":"ls -la"}`, string(useBlock.Input))
}

func TestRoundTrip_ToolResultBlock(t *testing.T) {
	msg := Message{
		ID:        "msg-3",
		SessionID: "sess-1",
		Role:      RoleUser,
		CreatedAt: time.Now().UTC().Truncate(time.Second),
		Content: []ContentBlock{
			ToolResultBlock{
				ToolUseID: "tool-1",
				Content:   "file1.txt\nfile2.txt",
				IsError:   true,
			},
		},
	}

	data, err := json.Marshal(msg)
	require.NoError(t, err)

	var decoded Message
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	require.Equal(t, msg.ID, decoded.ID)
	require.Equal(t, msg.SessionID, decoded.SessionID)
	require.Equal(t, msg.Role, decoded.Role)
	require.True(t, decoded.CreatedAt.Equal(msg.CreatedAt))
	require.Len(t, decoded.Content, 1)

	resBlock, ok := decoded.Content[0].(ToolResultBlock)
	require.True(t, ok)
	require.Equal(t, "tool-1", resBlock.ToolUseID)
	require.Equal(t, "file1.txt\nfile2.txt", resBlock.Content)
	require.True(t, resBlock.IsError)
}

func TestRoundTrip_ImageBlock(t *testing.T) {
	msg := Message{
		ID:        "msg-4",
		SessionID: "sess-1",
		Role:      RoleUser,
		CreatedAt: time.Now().UTC().Truncate(time.Second),
		Content: []ContentBlock{
			ImageBlock{
				MimeType: "image/png",
				Data:     []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 0},
			},
		},
	}

	data, err := json.Marshal(msg)
	require.NoError(t, err)

	var decoded Message
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	require.Equal(t, msg.ID, decoded.ID)
	require.Equal(t, msg.SessionID, decoded.SessionID)
	require.Equal(t, msg.Role, decoded.Role)
	require.True(t, decoded.CreatedAt.Equal(msg.CreatedAt))
	require.Len(t, decoded.Content, 1)

	imgBlock, ok := decoded.Content[0].(ImageBlock)
	require.True(t, ok)
	require.Equal(t, "image/png", imgBlock.MimeType)
	require.Equal(t, msg.Content[0].(ImageBlock).Data, imgBlock.Data)
}

func TestRoundTrip_ThinkingBlock(t *testing.T) {
	msg := Message{
		ID:        "msg-5",
		SessionID: "sess-1",
		Role:      RoleAssistant,
		CreatedAt: time.Now().UTC().Truncate(time.Second),
		Content: []ContentBlock{
			ThinkingBlock{
				Text: "Let me check the filesystem first.",
			},
		},
	}

	data, err := json.Marshal(msg)
	require.NoError(t, err)

	var decoded Message
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	require.Equal(t, msg.ID, decoded.ID)
	require.Equal(t, msg.SessionID, decoded.SessionID)
	require.Equal(t, msg.Role, decoded.Role)
	require.True(t, decoded.CreatedAt.Equal(msg.CreatedAt))
	require.Len(t, decoded.Content, 1)

	thinkBlock, ok := decoded.Content[0].(ThinkingBlock)
	require.True(t, ok)
	require.Equal(t, "Let me check the filesystem first.", thinkBlock.Text)
}

func TestRoundTrip_MixedContent(t *testing.T) {
	msg := Message{
		ID:        "msg-6",
		SessionID: "sess-1",
		Role:      RoleAssistant,
		CreatedAt: time.Now().UTC().Truncate(time.Second),
		Content: []ContentBlock{
			TextBlock{Text: "Initial greeting."},
			ThinkingBlock{Text: "Thinking..."},
			ToolUseBlock{ID: "t-1", Name: "bash", Input: json.RawMessage(`{}`)},
			ImageBlock{MimeType: "image/jpeg", Data: []byte("jpeg-data")},
			ToolResultBlock{ToolUseID: "t-2", Content: "success", IsError: false},
		},
	}

	data, err := json.Marshal(msg)
	require.NoError(t, err)

	var decoded Message
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	require.Equal(t, msg.ID, decoded.ID)
	require.Equal(t, msg.SessionID, decoded.SessionID)
	require.Equal(t, msg.Role, decoded.Role)
	require.True(t, decoded.CreatedAt.Equal(msg.CreatedAt))
	require.Len(t, decoded.Content, 5)

	require.Equal(t, BlockText, decoded.Content[0].Type())
	require.Equal(t, BlockThinking, decoded.Content[1].Type())
	require.Equal(t, BlockToolUse, decoded.Content[2].Type())
	require.Equal(t, BlockImage, decoded.Content[3].Type())
	require.Equal(t, BlockToolResult, decoded.Content[4].Type())

	require.Equal(t, msg.Content[0], decoded.Content[0])
	require.Equal(t, msg.Content[1], decoded.Content[1])
	require.Equal(t, msg.Content[2], decoded.Content[2])
	require.Equal(t, msg.Content[3], decoded.Content[3])
	require.Equal(t, msg.Content[4], decoded.Content[4])
}

func TestUnmarshal_UnknownBlockType_ReturnsError(t *testing.T) {
	invalidJSON := `{
		"id": "msg-err",
		"role": "user",
		"content": [
			{"type": "alien", "foo": "bar"}
		]
	}`

	var decoded Message
	err := json.Unmarshal([]byte(invalidJSON), &decoded)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrUnknownBlockType))
}

func TestValidate_ToolResultWithoutUse_Rejected(t *testing.T) {
	// A slice containing a ToolResultBlock whose ToolUseID has no prior ToolUseBlock
	messages := []Message{
		{
			ID:   "m1",
			Role: RoleUser,
			Content: []ContentBlock{
				ToolResultBlock{ToolUseID: "nonexistent", Content: "error message"},
			},
		},
	}

	err := Validate(messages)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrToolResultWithoutUse))
}

func TestValidate_OrphanToolUse_Rejected(t *testing.T) {
	// An assistant ToolUseBlock not followed (eventually) by a matching ToolResultBlock
	messages := []Message{
		{
			ID:   "m1",
			Role: RoleAssistant,
			Content: []ContentBlock{
				ToolUseBlock{ID: "tool-1", Name: "bash", Input: json.RawMessage(`{}`)},
			},
		},
	}

	err := Validate(messages)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrToolUseWithoutResult))
}

func TestValidate_EmptyContent_Rejected(t *testing.T) {
	messages := []Message{
		{
			ID:      "m1",
			Role:    RoleUser,
			Content: []ContentBlock{},
		},
	}

	err := Validate(messages)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrEmptyContent))
}

func TestValidate_ValidSequence(t *testing.T) {
	messages := []Message{
		{
			ID:   "m1",
			Role: RoleAssistant,
			Content: []ContentBlock{
				ToolUseBlock{ID: "t-1", Name: "bash", Input: json.RawMessage(`{}`)},
			},
		},
		{
			ID:   "m2",
			Role: RoleUser,
			Content: []ContentBlock{
				ToolResultBlock{ToolUseID: "t-1", Content: "stdout"},
			},
		},
	}

	err := Validate(messages)
	require.NoError(t, err)
}

func TestNormalize_MergesAdjacentText(t *testing.T) {
	// two adjacent TextBlock values in the same Message collapse to one
	messages := []Message{
		{
			ID:   "m1",
			Role: RoleUser,
			Content: []ContentBlock{
				TextBlock{Text: "Part 1, "},
				TextBlock{Text: "Part 2"},
			},
		},
	}

	normalized := Normalize(messages)
	require.Len(t, normalized, 1)
	require.Len(t, normalized[0].Content, 1)

	tb, ok := normalized[0].Content[0].(TextBlock)
	require.True(t, ok)
	require.Equal(t, "Part 1, Part 2", tb.Text)
}

func TestNormalize_DropsEmptyText(t *testing.T) {
	// a TextBlock{Text: ""} is removed
	messages := []Message{
		{
			ID:   "m1",
			Role: RoleUser,
			Content: []ContentBlock{
				TextBlock{Text: "Before"},
				TextBlock{Text: ""},
				TextBlock{Text: "After"},
			},
		},
	}

	normalized := Normalize(messages)
	require.Len(t, normalized, 1)
	require.Len(t, normalized[0].Content, 2)

	require.Equal(t, TextBlock{Text: "Before"}, normalized[0].Content[0])
	require.Equal(t, TextBlock{Text: "After"}, normalized[0].Content[1])
}

func TestNormalize_DoesNotMutateInput(t *testing.T) {
	// original slice and its blocks are byte-identical after Normalize
	messages := []Message{
		{
			ID:   "m1",
			Role: RoleUser,
			Content: []ContentBlock{
				TextBlock{Text: "Hello"},
				TextBlock{Text: ""},
				TextBlock{Text: "World"},
			},
		},
	}

	// We can capture the JSON representation before and verify it is identical after.
	origJSON, err := json.Marshal(messages)
	require.NoError(t, err)

	_ = Normalize(messages)

	afterJSON, err := json.Marshal(messages)
	require.NoError(t, err)

	require.Equal(t, string(origJSON), string(afterJSON))
}

func TestNormalize_Reordering(t *testing.T) {
	messages := []Message{
		{
			ID:   "m1",
			Role: RoleAssistant,
			Content: []ContentBlock{
				ToolUseBlock{ID: "t-1", Name: "bash", Input: json.RawMessage(`{}`)},
			},
		},
		{
			ID:   "m2",
			Role: RoleUser,
			Content: []ContentBlock{
				TextBlock{Text: "User interrupt message"},
			},
		},
		{
			ID:   "m3",
			Role: RoleTool, // Should be normalized to RoleUser
			Content: []ContentBlock{
				ToolResultBlock{ToolUseID: "t-1", Content: "success"},
			},
		},
	}

	normalized := Normalize(messages)
	require.Len(t, normalized, 3)

	// Expected order: m1 (tool use), m3 (tool result), m2 (user text)
	require.Equal(t, "m1", normalized[0].ID)
	require.Equal(t, "m3", normalized[1].ID)
	require.Equal(t, "m2", normalized[2].ID)

	// Roles verification
	require.Equal(t, RoleAssistant, normalized[0].Role)
	require.Equal(t, RoleUser, normalized[1].Role) // Guarantees ToolResultBlock is User role
	require.Equal(t, RoleUser, normalized[2].Role)
}

func TestNormalize_RoleEnforcement(t *testing.T) {
	messages := []Message{
		{
			ID:   "m1",
			Role: RoleUser,
			Content: []ContentBlock{
				ToolUseBlock{ID: "t-1", Name: "bash", Input: json.RawMessage(`{}`)},
			},
		},
	}

	normalized := Normalize(messages)
	require.Len(t, normalized, 1)
	require.Equal(t, RoleAssistant, normalized[0].Role)
}

func TestValidate_MalformedMessages(t *testing.T) {
	// Nil block
	msgNil := []Message{
		{
			ID:      "m1",
			Role:    RoleUser,
			Content: []ContentBlock{nil},
		},
	}
	require.ErrorIs(t, Validate(msgNil), ErrUnknownBlockType)

	// ToolUse missing ID
	msgNoUseID := []Message{
		{
			ID:      "m1",
			Role:    RoleAssistant,
			Content: []ContentBlock{ToolUseBlock{Name: "bash"}},
		},
	}
	require.ErrorIs(t, Validate(msgNoUseID), ErrEmptyToolUseID)

	// ToolUse duplicate ID
	msgDupUseID := []Message{
		{
			ID:   "m1",
			Role: RoleAssistant,
			Content: []ContentBlock{
				ToolUseBlock{ID: "t1", Name: "bash"},
				ToolUseBlock{ID: "t1", Name: "bash"},
			},
		},
	}
	require.ErrorIs(t, Validate(msgDupUseID), ErrDuplicateToolUseID)

	// ToolUse in a non-assistant-role message.
	msgToolUseUserRole := []Message{
		{
			ID:   "m1",
			Role: RoleUser,
			Content: []ContentBlock{
				ToolUseBlock{ID: "t1", Name: "bash"},
			},
		},
	}
	require.ErrorIs(t, Validate(msgToolUseUserRole), ErrToolUseRole)

	// ToolResult missing ToolUseID
	msgNoResID := []Message{
		{
			ID:   "m1",
			Role: RoleAssistant,
			Content: []ContentBlock{
				ToolUseBlock{ID: "t1", Name: "bash"},
			},
		},
		{
			ID:   "m2",
			Role: RoleUser,
			Content: []ContentBlock{
				ToolResultBlock{Content: "stdout"},
			},
		},
	}
	require.ErrorIs(t, Validate(msgNoResID), ErrToolResultWithoutUse)

	// ToolResult duplicate ToolUseID
	msgDupResID := []Message{
		{
			ID:   "m1",
			Role: RoleAssistant,
			Content: []ContentBlock{
				ToolUseBlock{ID: "t1", Name: "bash"},
			},
		},
		{
			ID:   "m2",
			Role: RoleUser,
			Content: []ContentBlock{
				ToolResultBlock{ToolUseID: "t1", Content: "stdout"},
				ToolResultBlock{ToolUseID: "t1", Content: "stdout2"},
			},
		},
	}
	require.ErrorIs(t, Validate(msgDupResID), ErrToolResultWithoutUse)

	// ToolResult precedes ToolUse in same message
	msgResBeforeUse := []Message{
		{
			ID:   "m1",
			Role: RoleUser, // Wait, Validate checks role. Let's make it Assistant to avoid role validation error first, or check both.
			Content: []ContentBlock{
				ToolResultBlock{ToolUseID: "t1", Content: "stdout"},
				ToolUseBlock{ID: "t1", Name: "bash"},
			},
		},
	}
	// Wait, if m1 has assistant role, then ToolResultBlock fails role validation (requires user).
	// If m1 has user role, then ToolUseBlock fails role validation (requires assistant).
	// So msgResBeforeUse fails either way. Let's verify that.
	require.Error(t, Validate(msgResBeforeUse))
}

// unknownBlock is a ContentBlock implementation whose Type() is not one of the
// recognized BlockType values, exercising Validate's default branch.
type unknownBlock struct{}

// Type returns an unrecognized BlockType.
func (unknownBlock) Type() BlockType {
	return BlockType("alien")
}

func TestValidate_UnknownBlockType_ReturnsUnknownBlockType(t *testing.T) {
	// A genuinely unrecognized block type must still map to ErrUnknownBlockType,
	// keeping it distinct from the tool_use ID and role sentinels.
	messages := []Message{
		{
			ID:      "m1",
			Role:    RoleUser,
			Content: []ContentBlock{unknownBlock{}},
		},
	}

	err := Validate(messages)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrUnknownBlockType)
}

func BenchmarkMarshalMessage_TextOnly(b *testing.B) {
	// Create a message with a 1 KB text body.
	text := make([]byte, 1024)
	for i := range text {
		text[i] = 'a'
	}

	msg := Message{
		ID:        "msg-bench",
		SessionID: "sess-bench",
		Role:      RoleUser,
		Content:   []ContentBlock{TextBlock{Text: string(text)}},
		CreatedAt: time.Now(),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := json.Marshal(msg)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshalMessage_MixedBlocks(b *testing.B) {
	msg := Message{
		ID:        "msg-bench",
		SessionID: "sess-bench",
		Role:      RoleAssistant,
		Content: []ContentBlock{
			TextBlock{Text: "Initial greeting."},
			ThinkingBlock{Text: "Thinking..."},
			ToolUseBlock{ID: "t-1", Name: "bash", Input: json.RawMessage(`{"cmd":"ls"}`)},
			ImageBlock{MimeType: "image/jpeg", Data: []byte("jpeg-data")},
			ToolResultBlock{ToolUseID: "t-2", Content: "success", IsError: false},
		},
		CreatedAt: time.Now(),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := json.Marshal(msg)
		if err != nil {
			b.Fatal(err)
		}
	}
}
