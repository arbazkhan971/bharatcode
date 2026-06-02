// Package message defines the canonical conversation representation for BharatCode.
// It is provider-agnostic and enforces structural invariants on messages.
package message

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Role is the conversational role of a Message.
type Role string

const (
	// RoleUser represents a message from the user.
	RoleUser Role = "user"
	// RoleAssistant represents a message from the assistant (model).
	RoleAssistant Role = "assistant"
	// RoleSystem represents a system message directing the model.
	RoleSystem Role = "system"
	// RoleTool represents a message carrying tool results.
	RoleTool Role = "tool"
)

// BlockType discriminates ContentBlock implementations on the wire.
type BlockType string

const (
	// BlockText represents a plain text block.
	BlockText BlockType = "text"
	// BlockToolUse represents a tool execution request from the model.
	BlockToolUse BlockType = "tool_use"
	// BlockToolResult represents the execution result of a tool.
	BlockToolResult BlockType = "tool_result"
	// BlockImage represents an inline image block.
	BlockImage BlockType = "image"
	// BlockThinking represents reasoning traces from the provider.
	BlockThinking BlockType = "thinking"
)

// ContentBlock is one typed segment of a Message body.
// All concrete blocks marshal to JSON with a "type" discriminator.
type ContentBlock interface {
	Type() BlockType
}

// TextBlock is a plain-text segment.
type TextBlock struct {
	Text string `json:"text"`
}

// Type returns BlockText.
func (b TextBlock) Type() BlockType {
	return BlockText
}

// MarshalJSON serializes TextBlock with its type discriminator.
func (b TextBlock) MarshalJSON() ([]byte, error) {
	type Alias TextBlock
	return json.Marshal(&struct {
		Type BlockType `json:"type"`
		Alias
	}{
		Type:  BlockText,
		Alias: Alias(b),
	})
}

// ToolUseBlock is a model's request to invoke a tool.
// Input is opaque JSON forwarded to the tool implementation.
type ToolUseBlock struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// Type returns BlockToolUse.
func (b ToolUseBlock) Type() BlockType {
	return BlockToolUse
}

// MarshalJSON serializes ToolUseBlock with its type discriminator.
func (b ToolUseBlock) MarshalJSON() ([]byte, error) {
	type Alias ToolUseBlock
	return json.Marshal(&struct {
		Type BlockType `json:"type"`
		Alias
	}{
		Type:  BlockToolUse,
		Alias: Alias(b),
	})
}

// ToolResultBlock is the response that closes a prior ToolUseBlock.
// Content is the tool's stringified output. IsError is true when
// the tool reported a failure the model should observe.
type ToolResultBlock struct {
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error"`
}

// Type returns BlockToolResult.
func (b ToolResultBlock) Type() BlockType {
	return BlockToolResult
}

// MarshalJSON serializes ToolResultBlock with its type discriminator.
func (b ToolResultBlock) MarshalJSON() ([]byte, error) {
	type Alias ToolResultBlock
	return json.Marshal(&struct {
		Type BlockType `json:"type"`
		Alias
	}{
		Type:  BlockToolResult,
		Alias: Alias(b),
	})
}

// ImageBlock carries inline base64 image data.
type ImageBlock struct {
	MimeType string `json:"mime_type"`
	Data     []byte `json:"data"`
}

// Type returns BlockImage.
func (b ImageBlock) Type() BlockType {
	return BlockImage
}

// MarshalJSON serializes ImageBlock with its type discriminator.
func (b ImageBlock) MarshalJSON() ([]byte, error) {
	type Alias ImageBlock
	return json.Marshal(&struct {
		Type BlockType `json:"type"`
		Alias
	}{
		Type:  BlockImage,
		Alias: Alias(b),
	})
}

// ThinkingBlock carries provider reasoning traces (Anthropic
// extended thinking, OpenAI o-series reasoning, etc.).
type ThinkingBlock struct {
	Text string `json:"text"`
}

// Type returns BlockThinking.
func (b ThinkingBlock) Type() BlockType {
	return BlockThinking
}

// MarshalJSON serializes ThinkingBlock with its type discriminator.
func (b ThinkingBlock) MarshalJSON() ([]byte, error) {
	type Alias ThinkingBlock
	return json.Marshal(&struct {
		Type BlockType `json:"type"`
		Alias
	}{
		Type:  BlockThinking,
		Alias: Alias(b),
	})
}

// TokenUsage records provider-reported token counts for a Message.
type TokenUsage struct {
	InputTokens      int `json:"input_tokens"`
	OutputTokens     int `json:"output_tokens"`
	CacheReadTokens  int `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
}

// Message is one entry in a session conversation.
type Message struct {
	ID        string         `json:"id"`
	SessionID string         `json:"session_id"`
	Role      Role           `json:"role"`
	Content   []ContentBlock `json:"content"`
	ParentID  *string        `json:"parent_id,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	Usage     *TokenUsage    `json:"usage,omitempty"`
}

// Sentinel errors returned by Validate or during deserialization.
var (
	ErrToolResultWithoutUse = errors.New("tool_result without preceding tool_use")
	ErrToolUseWithoutResult = errors.New("tool_use without following tool_result")
	ErrEmptyContent         = errors.New("message has no content blocks")
	ErrUnknownBlockType     = errors.New("unknown content block type")
	// ErrEmptyToolUseID indicates a tool_use block with an empty ID.
	ErrEmptyToolUseID = errors.New("tool_use block has empty ID")
	// ErrDuplicateToolUseID indicates two tool_use blocks share the same ID.
	ErrDuplicateToolUseID = errors.New("duplicate tool_use ID")
	// ErrToolUseRole indicates a tool_use block in a non-assistant-role message.
	ErrToolUseRole = errors.New("tool_use block requires assistant role")
)

// MarshalJSON serializes a Message into JSON, converting the Content slice
// into raw JSON messages to preserve concrete types and their discriminators.
func (m Message) MarshalJSON() ([]byte, error) {
	rawBlocks := make([]json.RawMessage, 0, len(m.Content))
	for i, block := range m.Content {
		raw, err := json.Marshal(block)
		if err != nil {
			return nil, fmt.Errorf("marshalling content block at index %d: %w", i, err)
		}
		rawBlocks = append(rawBlocks, raw)
	}

	aux := struct {
		ID        string            `json:"id"`
		SessionID string            `json:"session_id"`
		Role      Role              `json:"role"`
		Content   []json.RawMessage `json:"content"`
		ParentID  *string           `json:"parent_id,omitempty"`
		CreatedAt time.Time         `json:"created_at"`
		Usage     *TokenUsage       `json:"usage,omitempty"`
	}{
		ID:        m.ID,
		SessionID: m.SessionID,
		Role:      m.Role,
		Content:   rawBlocks,
		ParentID:  m.ParentID,
		CreatedAt: m.CreatedAt,
		Usage:     m.Usage,
	}

	return json.Marshal(aux)
}

// UnmarshalJSON deserializes a Message from JSON, dispatching each item in
// Content to its concrete type based on the "type" field.
func (m *Message) UnmarshalJSON(data []byte) error {
	aux := struct {
		ID        string            `json:"id"`
		SessionID string            `json:"session_id"`
		Role      Role              `json:"role"`
		Content   []json.RawMessage `json:"content"`
		ParentID  *string           `json:"parent_id,omitempty"`
		CreatedAt time.Time         `json:"created_at"`
		Usage     *TokenUsage       `json:"usage,omitempty"`
	}{}

	if err := json.Unmarshal(data, &aux); err != nil {
		return fmt.Errorf("unmarshalling message envelope: %w", err)
	}

	content := make([]ContentBlock, len(aux.Content))
	for i, raw := range aux.Content {
		var typeInfo struct {
			Type BlockType `json:"type"`
		}
		if err := json.Unmarshal(raw, &typeInfo); err != nil {
			return fmt.Errorf("unmarshalling block type at index %d: %w", i, err)
		}

		var block ContentBlock
		switch typeInfo.Type {
		case BlockText:
			var b TextBlock
			if err := json.Unmarshal(raw, &b); err != nil {
				return fmt.Errorf("unmarshalling text block at index %d: %w", i, err)
			}
			block = b
		case BlockToolUse:
			var b ToolUseBlock
			if err := json.Unmarshal(raw, &b); err != nil {
				return fmt.Errorf("unmarshalling tool_use block at index %d: %w", i, err)
			}
			block = b
		case BlockToolResult:
			var b ToolResultBlock
			if err := json.Unmarshal(raw, &b); err != nil {
				return fmt.Errorf("unmarshalling tool_result block at index %d: %w", i, err)
			}
			block = b
		case BlockImage:
			var b ImageBlock
			if err := json.Unmarshal(raw, &b); err != nil {
				return fmt.Errorf("unmarshalling image block at index %d: %w", i, err)
			}
			block = b
		case BlockThinking:
			var b ThinkingBlock
			if err := json.Unmarshal(raw, &b); err != nil {
				return fmt.Errorf("unmarshalling thinking block at index %d: %w", i, err)
			}
			block = b
		default:
			return fmt.Errorf("decoding block %q: %w", string(raw), ErrUnknownBlockType)
		}
		content[i] = block
	}

	m.ID = aux.ID
	m.SessionID = aux.SessionID
	m.Role = aux.Role
	m.Content = content
	m.ParentID = aux.ParentID
	m.CreatedAt = aux.CreatedAt
	m.Usage = aux.Usage

	return nil
}
