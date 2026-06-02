// Package pubsub provides a generic, in-process, per-topic event bus.
package pubsub

// MessagePayload is a placeholder for message.Event.
type MessagePayload struct{}

// ToolCallPayload is a placeholder for tools.CallEvent.
type ToolCallPayload struct{}

// LSPDiagnosticPayload is a placeholder for lsp.DiagnosticEvent.
type LSPDiagnosticPayload struct{}

// ShellJobPayload carries lifecycle events for background shell jobs
// (started, stdout chunk, stderr chunk, exited).
type ShellJobPayload struct {
	JobID  string
	Stream string // "stdout" or "stderr"
	Chunk  []byte
	Done   bool
}

// LedgerUpdatePayload is a placeholder for ledger.UpdateEvent.
type LedgerUpdatePayload struct{}
