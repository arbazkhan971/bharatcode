// Package pubsub provides a generic, in-process, per-topic event bus.
package pubsub

// Topics declared here are the canonical bus endpoints used by the rest of
// BharatCode. New cross-module event flows should add a topic here rather than
// spinning up an ad-hoc *Topic instance in the producer.
var (
	// MessageEvents carries assistant/user/tool messages produced by the agent
	// loop.
	// Subscribers: TUI chat view.
	// Payload type: message.Event.
	MessageEvents = NewTopic[MessagePayload]("messages", 0)

	// ToolCallEvents carries tool-call start/end records.
	// Subscribers: TUI tool-call panel, ledger cost accumulator.
	// Payload type: tools.CallEvent.
	ToolCallEvents = NewTopic[ToolCallPayload]("tool_calls", 0)

	// LSPDiagnosticEvents carries diagnostics emitted by language servers.
	// Subscribers: TUI status bar, agent context builder.
	// Payload type: lsp.DiagnosticEvent.
	LSPDiagnosticEvents = NewTopic[LSPDiagnosticPayload]("lsp_diagnostics", 256)

	// ShellJobEvents carries lifecycle events for background shell jobs
	// (started, stdout chunk, stderr chunk, exited).
	// Subscribers: TUI background-jobs panel.
	// Payload type: shell.JobEvent.
	ShellJobEvents = NewTopic[ShellJobPayload]("shell_jobs", 0)

	// PermissionRequests is request/response, not fan-out. The agent
	// publishes a PermissionRequest carrying a Reply channel; the TUI
	// subscribes, prompts the user, and sends the decision back on Reply.
	// There is exactly one subscriber (the TUI in interactive mode, or the
	// --yolo auto-approver in headless mode). The bus is reused so headless
	// mode can swap subscribers without changing producer code.
	// Payload type: PermissionRequest.
	PermissionRequests = NewTopic[PermissionRequest]("permissions", 16)

	// LedgerUpdateEvents carries every appended ledger entry plus a running
	// session/day/month total.
	// Subscribers: TUI footer.
	// Payload type: ledger.UpdateEvent.
	LedgerUpdateEvents = NewTopic[LedgerUpdatePayload]("ledger", 64)
)

// PermissionRequest is the payload type for PermissionRequests. The agent
// fills Tool, Args, Reason and a fresh Reply channel, then publishes. The
// handler sends exactly one PermissionDecision on Reply and the agent blocks
// on <-Reply.
type PermissionRequest struct {
	// Tool is the name of the tool requesting permission.
	Tool string
	// Args contains the arguments passed to the tool.
	Args map[string]any
	// Reason is the reason given by the agent for invoking this tool.
	Reason string
	// Reply is a channel to send the decision back to the requester.
	// Must be buffered with capacity 1.
	Reply chan PermissionDecision
}

// PermissionDecision is the reply value sent back on
// PermissionRequest.Reply.
type PermissionDecision struct {
	// Approved indicates whether the tool execution was approved by the user.
	Approved bool
	// Remember indicates whether this decision should be remembered for this
	// session.
	Remember bool
	// Reason is an optional reason for the decision, particularly useful on
	// denial.
	Reason string
}
