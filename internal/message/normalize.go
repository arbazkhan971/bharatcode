// Package message defines the canonical conversation representation for BharatCode.
package message

import (
	"fmt"
	"log/slog"
)

// Normalize applies BharatCode's structural invariants to a slice of
// Messages and returns the normalized slice. It:
//   - merges adjacent TextBlocks in the same Message,
//   - drops empty TextBlocks,
//   - reorders so every ToolResultBlock immediately follows the
//     ToolUseBlock with the matching ID,
//   - guarantees ToolResultBlocks live in a user-role Message and
//     ToolUseBlocks in an assistant-role Message.
//
// Normalize never mutates the input slice.
func Normalize(messages []Message) []Message {
	if len(messages) == 0 {
		return nil
	}

	// 1. Deep copy the messages.
	copied := make([]Message, len(messages))
	for i, m := range messages {
		copied[i] = Message{
			ID:        m.ID,
			SessionID: m.SessionID,
			Role:      m.Role,
			ParentID:  m.ParentID,
			CreatedAt: m.CreatedAt,
		}
		if m.Usage != nil {
			usageCopy := *m.Usage
			copied[i].Usage = &usageCopy
		}
		if m.Content != nil {
			contentCopy := make([]ContentBlock, len(m.Content))
			copy(contentCopy, m.Content)
			copied[i].Content = contentCopy
		}
	}

	// 2. Clean block lists (merge adjacent text blocks, drop empty text blocks).
	for i := range copied {
		var cleaned []ContentBlock
		var lastWasText bool
		for _, block := range copied[i].Content {
			tb, isText := block.(TextBlock)
			if isText && tb.Text == "" {
				slog.Warn("Dropping empty text block", "session_id", copied[i].SessionID)
				lastWasText = false
				continue
			}

			if isText {
				if lastWasText && len(cleaned) > 0 {
					if prevTb, ok := cleaned[len(cleaned)-1].(TextBlock); ok {
						cleaned[len(cleaned)-1] = TextBlock{
							Text: prevTb.Text + tb.Text,
						}
						continue
					}
				}
				lastWasText = true
			} else {
				lastWasText = false
			}
			cleaned = append(cleaned, block)
		}
		copied[i].Content = cleaned
	}

	// 3. Guarantee ToolResultBlocks live in a user-role Message and
	// ToolUseBlocks in an assistant-role Message.
	for i := range copied {
		hasToolUse := false
		hasToolResult := false
		for _, block := range copied[i].Content {
			switch block.Type() {
			case BlockToolUse:
				hasToolUse = true
			case BlockToolResult:
				hasToolResult = true
			}
		}
		if hasToolUse {
			copied[i].Role = RoleAssistant
		}
		if hasToolResult {
			copied[i].Role = RoleUser
		}
	}

	// 4. Drop messages that became empty after dropping empty TextBlocks.
	var nonCtxEmpty []Message
	for _, m := range copied {
		if len(m.Content) > 0 {
			nonCtxEmpty = append(nonCtxEmpty, m)
		}
	}
	copied = nonCtxEmpty

	// 5. Reorder so every ToolResultBlock immediately follows the
	// ToolUseBlock with the matching ID.
	placed := make([]bool, len(copied))
	var result []Message

	var placeDeps func(msg Message)
	placeDeps = func(msg Message) {
		for _, block := range msg.Content {
			if useBlock, ok := block.(ToolUseBlock); ok {
				for j, m2 := range copied {
					if placed[j] {
						continue
					}
					hasMatchingResult := false
					for _, block2 := range m2.Content {
						if resBlock, ok := block2.(ToolResultBlock); ok && resBlock.ToolUseID == useBlock.ID {
							hasMatchingResult = true
							break
						}
					}
					if hasMatchingResult {
						result = append(result, m2)
						placed[j] = true
						placeDeps(m2)
						break
					}
				}
			}
		}
	}

	for i, m := range copied {
		if placed[i] {
			continue
		}
		result = append(result, m)
		placed[i] = true
		placeDeps(m)
	}

	return result
}

// Validate returns a non-nil error if the slice violates the invariants
// Normalize enforces. Callers that received messages from an external
// source should Validate before persisting.
func Validate(messages []Message) error {
	if len(messages) == 0 {
		return nil
	}

	type blockLoc struct {
		msgIdx   int
		blockIdx int
	}

	toolUses := make(map[string]blockLoc)
	toolResults := make(map[string]blockLoc)

	for i, m := range messages {
		if len(m.Content) == 0 {
			return fmt.Errorf("message has empty content: %w", ErrEmptyContent)
		}

		for j, block := range m.Content {
			if block == nil {
				return fmt.Errorf("block is nil at message %d, block %d: %w", i, j, ErrUnknownBlockType)
			}

			switch b := block.(type) {
			case ToolUseBlock:
				if b.ID == "" {
					return fmt.Errorf("tool_use ID is empty at message %d, block %d: %w", i, j, ErrEmptyToolUseID)
				}
				if _, exists := toolUses[b.ID]; exists {
					return fmt.Errorf("duplicate tool_use ID %q: %w", b.ID, ErrDuplicateToolUseID)
				}
				if m.Role != RoleAssistant {
					return fmt.Errorf("tool_use at message %d requires assistant role: %w", i, ErrToolUseRole)
				}
				toolUses[b.ID] = blockLoc{msgIdx: i, blockIdx: j}

			case ToolResultBlock:
				if b.ToolUseID == "" {
					return fmt.Errorf("tool_result has empty ToolUseID at message %d, block %d: %w", i, j, ErrToolResultWithoutUse)
				}
				if _, exists := toolResults[b.ToolUseID]; exists {
					return fmt.Errorf("duplicate tool_result for %q: %w", b.ToolUseID, ErrToolResultWithoutUse)
				}
				if m.Role != RoleUser {
					return fmt.Errorf("tool_result at message %d requires user role: %w", i, ErrToolResultWithoutUse)
				}
				toolResults[b.ToolUseID] = blockLoc{msgIdx: i, blockIdx: j}

			case TextBlock:
				// No extra validation required.
			case ImageBlock:
				// No extra validation required.
			case ThinkingBlock:
				// No extra validation required.
			default:
				return fmt.Errorf("unrecognized type %T: %w", block, ErrUnknownBlockType)
			}
		}
	}

	// Verify pairing and ordering.
	for useID, useLoc := range toolUses {
		resLoc, hasResult := toolResults[useID]
		if !hasResult {
			return fmt.Errorf("missing tool_result for tool_use %q: %w", useID, ErrToolUseWithoutResult)
		}
		if resLoc.msgIdx < useLoc.msgIdx {
			return fmt.Errorf("tool_result for %q at message %d precedes tool_use at message %d: %w", useID, resLoc.msgIdx, useLoc.msgIdx, ErrToolResultWithoutUse)
		}
		if resLoc.msgIdx == useLoc.msgIdx && resLoc.blockIdx <= useLoc.blockIdx {
			return fmt.Errorf("tool_result for %q precedes tool_use inside same message: %w", useID, ErrToolResultWithoutUse)
		}
	}

	for resID := range toolResults {
		if _, hasUse := toolUses[resID]; !hasUse {
			return fmt.Errorf("tool_result for %q has no matching tool_use: %w", resID, ErrToolResultWithoutUse)
		}
	}

	return nil
}
