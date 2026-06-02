package cmd

import (
	"github.com/arbazkhan971/bharatcode/internal/agent"
	"github.com/arbazkhan971/bharatcode/internal/message"
)

// runEvent is the NDJSON shape emitted by "bharatcode run --json", one object
// per agent.Event observed on the agent bus.
type runEvent struct {
	// Type is the snake_case event kind (turn_started, llm_response, ...).
	Type string `json:"type"`
	// SessionID is the session the event belongs to.
	SessionID string `json:"session_id,omitempty"`
	// Agent is the name of the agent that produced the event.
	Agent string `json:"agent,omitempty"`
	// Text carries the assistant text for llm_response events.
	Text string `json:"text,omitempty"`
	// Tool is the tool name for tool_called and tool_result events.
	Tool string `json:"tool,omitempty"`
	// Error carries the error string for run_error events.
	Error string `json:"error,omitempty"`
}

// eventTypeNames maps agent.EventKind to its snake_case wire name.
var eventTypeNames = map[agent.EventKind]string{
	agent.EventTurnStarted:  "turn_started",
	agent.EventLLMResponse:  "llm_response",
	agent.EventToolCalled:   "tool_called",
	agent.EventToolResult:   "tool_result",
	agent.EventLoopDetected: "loop_detected",
	agent.EventTurnFinished: "turn_finished",
	agent.EventRunError:     "run_error",
}

// eventTypeName returns the snake_case wire name for kind, or "unknown".
func eventTypeName(kind agent.EventKind) string {
	if name, ok := eventTypeNames[kind]; ok {
		return name
	}
	return "unknown"
}

// newRunEvent maps an agent.Event to its NDJSON wire shape. Text is populated
// only for llm_response (and loop_detected) events, Tool only for tool events,
// and Error only for run_error events.
func newRunEvent(ev agent.Event) runEvent {
	out := runEvent{
		Type:      eventTypeName(ev.Kind),
		SessionID: ev.SessionID,
		Agent:     ev.AgentName,
		Tool:      ev.ToolName,
	}
	switch ev.Kind {
	case agent.EventLLMResponse, agent.EventLoopDetected, agent.EventTurnFinished:
		out.Text = eventMessageText(ev.Message)
	case agent.EventRunError:
		if ev.Err != nil {
			out.Error = ev.Err.Error()
		}
	}
	return out
}

// eventMessageText returns the concatenated text of an event's optional
// message, or the empty string when the event carries no message.
func eventMessageText(msg *message.Message) string {
	if msg == nil {
		return ""
	}
	return messageText(*msg)
}
