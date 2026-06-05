// Package agent implements the agent loop, prompt assembly, and named-agent
// coordination.
package agent

import (
	"errors"

	"github.com/arbazkhan971/bharatcode/internal/message"
)

// EventKind enumerates agent event variants.
type EventKind int

const (
	// EventTurnStarted indicates a user turn began.
	EventTurnStarted EventKind = iota
	// EventLLMResponse indicates assistant output was received.
	EventLLMResponse
	// EventToolCalled indicates a tool call is about to run.
	EventToolCalled
	// EventToolResult indicates a tool returned a result.
	EventToolResult
	// EventLoopDetected indicates repeated tool calls tripped the loop guard.
	EventLoopDetected
	// EventTurnFinished indicates a turn completed.
	EventTurnFinished
	// EventRunError indicates an infrastructure error or recovered panic.
	EventRunError
)

// Event is published for significant agent-loop transitions.
type Event struct {
	SessionID string
	AgentName string
	Kind      EventKind
	Message   *message.Message
	ToolName  string
	Err       error
}

// ErrUnknownAgent is returned when a requested named agent is not configured.
var ErrUnknownAgent = errors.New("unknown agent")

// ErrLoopDetected is folded into the session when repeated, non-progressing
// tool activity trips the guard: either the same call returning the same result
// three times, or two steps oscillating in an A,B,A,B cycle.
var ErrLoopDetected = errors.New("loop detected: repeated tool calls produced no progress")

// ErrContextOverflow is returned when the latest user message alone exceeds the
// model's usable context window, so no amount of compaction or drop-oldest
// truncation can make the turn fit. The loop returns it rather than looping
// indefinitely or silently sending an over-window request.
var ErrContextOverflow = errors.New("context overflow: latest user message exceeds the model context window")
