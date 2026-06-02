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

// ErrLoopDetected is folded into the session when repeated tool calls trip.
var ErrLoopDetected = errors.New("loop detected: 3 identical tool calls in a row")
