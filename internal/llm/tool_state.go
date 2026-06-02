package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
)

type toolCallState struct {
	byIndex map[int]*partialToolCall
}

type partialToolCall struct {
	id        string
	name      string
	arguments string
	started   bool
}

func newToolCallState() *toolCallState {
	return &toolCallState{byIndex: make(map[int]*partialToolCall)}
}

func (s *toolCallState) applyDelta(ctx context.Context, events chan<- Event, delta openAIToolCallDelta) {
	index := 0
	if delta.Index != nil {
		index = *delta.Index
	}
	call := s.byIndex[index]
	if call == nil {
		call = &partialToolCall{}
		s.byIndex[index] = call
	}
	if delta.ID != "" {
		call.id = delta.ID
	}
	if delta.Function.Name != "" {
		call.name = delta.Function.Name
	}
	if !call.started && (call.id != "" || call.name != "") {
		send(ctx, events, ToolUseStartEvent{ID: call.id, Name: call.name})
		call.started = true
	}
	if delta.Function.Arguments != "" {
		call.arguments += delta.Function.Arguments
		send(ctx, events, ToolUseDeltaEvent{
			ID:    call.id,
			Delta: delta.Function.Arguments,
		})
	}
}

func (s *toolCallState) endAll(ctx context.Context, events chan<- Event) {
	indexes := make([]int, 0, len(s.byIndex))
	for index := range s.byIndex {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	for _, index := range indexes {
		call := s.byIndex[index]
		if !call.started {
			continue
		}
		input := json.RawMessage(call.arguments)
		if len(input) == 0 {
			input = json.RawMessage(`{}`)
		}
		if !json.Valid(input) {
			input = json.RawMessage(fmt.Sprintf("%q", call.arguments))
		}
		send(ctx, events, ToolUseEndEvent{
			ID:    call.id,
			Name:  call.name,
			Input: input,
		})
	}
}
