package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/message"
)

const reservedResponseTokens = 4096

// compactionSummaryMarker prefixes the synthetic message that the default
// Compactor leaves in place of the dropped conversation history.
const compactionSummaryMarker = "[compacted history]"

// preservedFilesMarker prefixes the synthetic message that carries the set of
// files the session has read and edited across a compaction. The summarizer may
// drop tool-call detail, so this frame names the touched files explicitly — and
// is itself parsed back on the next compaction — so the agent never re-reads a
// file it already saw or clobbers one it already edited after older turns are
// condensed away.
const preservedFilesMarker = "[preserved files]"

// preservedReadHeading and preservedEditHeading label the two sections of the
// preserved-files frame. They are fixed strings so the frame can be parsed back
// on a subsequent compaction, keeping the census durable across repeated
// compactions.
const (
	preservedReadHeading = "Read in this session:"
	preservedEditHeading = "Edited in this session:"
)

// readToolNames and editToolNames classify a tool call as a file read or a file
// edit for the purpose of preserving touched files across compaction. The edit
// set mirrors the mutating tools that enforce read-before-edit.
var (
	readToolNames = map[string]struct{}{"view": {}}
	editToolNames = map[string]struct{}{
		"write": {}, "edit": {}, "multiedit": {}, "patch": {}, "rename": {}, "notebook_edit": {},
	}
)

// toolCallPath extracts the "path" argument from a tool call's JSON input, or ""
// when absent. It mirrors the inline decode the loop and file-edit hooks use.
func toolCallPath(input json.RawMessage) string {
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return ""
	}
	return args.Path
}

// patchToolPaths extracts the edited file paths from a patch tool call's JSON
// input. The patch tool's input is {"patch":"<unified diff>"} rather than
// {"path":"..."}, so toolCallPath returns "" for patch calls. This function
// parses the "+++ b/<path>" lines from the unified-diff value and returns the
// de-duplicated set of affected file paths in the order they first appear.
func patchToolPaths(input json.RawMessage) []string {
	var args struct {
		Patch string `json:"patch"`
	}
	if err := json.Unmarshal(input, &args); err != nil || args.Patch == "" {
		return nil
	}
	seen := map[string]bool{}
	var paths []string
	// lastOldPath holds the path parsed from the most recent "--- a/<path>"
	// header. A deletion hunk has "+++ /dev/null" as its new-file header, so
	// the old path is the only one available to record.
	lastOldPath := ""

	addPath := func(p string) {
		if p != "" && !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}

	parseDiffPath := func(prefix, line string) string {
		rest := strings.TrimPrefix(line, prefix)
		// Strip the "a/" or "b/" git diff prefix when present.
		if strings.HasPrefix(rest, "a/") || strings.HasPrefix(rest, "b/") {
			rest = rest[2:]
		}
		// Remove any trailing tab + timestamp that some diff generators emit.
		if idx := strings.IndexByte(rest, '\t'); idx >= 0 {
			rest = rest[:idx]
		}
		p := strings.TrimSpace(rest)
		if p == "/dev/null" {
			return ""
		}
		return p
	}

	for _, line := range strings.Split(args.Patch, "\n") {
		if strings.HasPrefix(line, "--- ") {
			// Track the old-file path in case the next "+++ " is /dev/null
			// (a deletion hunk), which would otherwise produce no path.
			lastOldPath = parseDiffPath("--- ", line)
			continue
		}
		if !strings.HasPrefix(line, "+++ ") {
			continue
		}
		// Unified diff new-file header: "+++ b/<path>" or "+++ <path>".
		newPath := parseDiffPath("+++ ", line)
		if newPath != "" {
			// Normal add or modify: record the new-file path.
			addPath(newPath)
		} else {
			// "+++ /dev/null" means deletion: fall back to the old-file path.
			addPath(lastOldPath)
		}
		lastOldPath = ""
	}
	return paths
}

// touchedFiles returns the de-duplicated, order-stable sets of files the session
// has read and edited, drawn from the agent's own tool calls in history plus any
// earlier preserved-files frame (so the census accretes across repeated
// compactions rather than being lost when the original tool calls are condensed
// away). A file that was edited is omitted from the read set, since editing it
// implies it was read.
func touchedFiles(history []message.Message) (read, edited []string) {
	readSeen := map[string]bool{}
	editSeen := map[string]bool{}
	var readOrder, editOrder []string
	addRead := func(p string) {
		if p != "" && !readSeen[p] {
			readSeen[p] = true
			readOrder = append(readOrder, p)
		}
	}
	addEdit := func(p string) {
		if p != "" && !editSeen[p] {
			editSeen[p] = true
			editOrder = append(editOrder, p)
		}
	}
	classify := func(name string, input json.RawMessage) {
		if _, ok := readToolNames[name]; ok {
			addRead(toolCallPath(input))
		}
		if _, ok := editToolNames[name]; ok {
			if name == "patch" {
				// The patch tool carries a unified diff in a "patch" key, not
				// a single "path". Extract all affected file paths from the diff.
				for _, p := range patchToolPaths(input) {
					addEdit(p)
				}
			} else {
				addEdit(toolCallPath(input))
			}
		}
	}
	for _, m := range history {
		for _, block := range m.Content {
			switch b := block.(type) {
			case message.ToolUseBlock:
				classify(b.Name, b.Input)
			case *message.ToolUseBlock:
				classify(b.Name, b.Input)
			case message.TextBlock:
				priorRead, priorEdited := parsePreservedFrame(b.Text)
				for _, p := range priorRead {
					addRead(p)
				}
				for _, p := range priorEdited {
					addEdit(p)
				}
			case *message.TextBlock:
				priorRead, priorEdited := parsePreservedFrame(b.Text)
				for _, p := range priorRead {
					addRead(p)
				}
				for _, p := range priorEdited {
					addEdit(p)
				}
			}
		}
	}
	edited = editOrder
	for _, p := range readOrder {
		if !editSeen[p] {
			read = append(read, p)
		}
	}
	return read, edited
}

// buildPreservedFrame renders the preserved-files frame text from the read and
// edited sets. It returns "" when both are empty. A section is omitted when its
// set is empty, but at least one section is always present when the frame is
// non-empty.
func buildPreservedFrame(read, edited []string) string {
	if len(read) == 0 && len(edited) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(preservedFilesMarker)
	b.WriteString("\nThese files were read or edited earlier in this session; keep them in mind after older turns were condensed away.\n")
	if len(edited) > 0 {
		b.WriteString("\n")
		b.WriteString(preservedEditHeading)
		b.WriteString("\n")
		for _, p := range edited {
			fmt.Fprintf(&b, "- %s\n", p)
		}
	}
	if len(read) > 0 {
		b.WriteString("\n")
		b.WriteString(preservedReadHeading)
		b.WriteString("\n")
		for _, p := range read {
			fmt.Fprintf(&b, "- %s\n", p)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// parsePreservedFrame parses a preserved-files frame's text back into its read
// and edited path lists. It returns nil, nil when text is not a preserved-files
// frame. It is the inverse of buildPreservedFrame, letting touchedFiles recover
// a prior frame's census so repeated compactions accrete rather than forget.
func parsePreservedFrame(text string) (read, edited []string) {
	if !strings.Contains(text, preservedFilesMarker) {
		return nil, nil
	}
	section := ""
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		switch trimmed {
		case preservedReadHeading:
			section = "read"
			continue
		case preservedEditHeading:
			section = "edit"
			continue
		}
		if !strings.HasPrefix(trimmed, "- ") {
			continue
		}
		path := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
		if path == "" {
			continue
		}
		switch section {
		case "read":
			read = append(read, path)
		case "edit":
			edited = append(edited, path)
		}
	}
	return read, edited
}

// isPreservedFrame reports whether msg is a preserved-files frame, identified by
// the marker in its text content.
func isPreservedFrame(msg message.Message) bool {
	return strings.Contains(textContent(msg), preservedFilesMarker)
}

// preserveTouchedFiles ensures the condensed history carries an up-to-date
// preserved-files frame derived from the full input history. It is called after
// the Compactor has run: it recomputes the read/edited census from history (and
// any prior frame folded in by touchedFiles), drops any stale frame left in
// condensed, and prepends a fresh frame at the front so it stays ahead of the
// genuine latest user message (preserving latest-user detection). When no files
// have been touched it returns condensed unchanged, so a text-only conversation
// is unaffected.
func preserveTouchedFiles(history, condensed []message.Message) []message.Message {
	read, edited := touchedFiles(history)
	frameText := buildPreservedFrame(read, edited)
	if frameText == "" {
		return condensed
	}
	out := make([]message.Message, 0, len(condensed)+1)
	out = append(out, message.Message{
		SessionID: sessionIDOf(history),
		Role:      message.RoleUser,
		Content:   []message.ContentBlock{message.TextBlock{Text: frameText}},
		CreatedAt: firstCreatedAt(history),
	})
	for _, m := range condensed {
		if isPreservedFrame(m) {
			continue
		}
		out = append(out, m)
	}
	return out
}

// firstCreatedAt returns the timestamp of the first message in history, used to
// stamp synthetic compaction frames so they sort ahead of the retained tail.
func firstCreatedAt(history []message.Message) time.Time {
	if len(history) > 0 {
		return history[0].CreatedAt
	}
	return time.Time{}
}

// toolResultSummaryLimit caps how many characters of a single tool result are
// serialized into the prefix the summarizer sees. Long tool outputs (file
// reads, verbose command logs) dominate the prefix while contributing little
// summary signal, so each one is truncated to keep the summarization request
// focused and bounded.
const toolResultSummaryLimit = 2000

// compactionSystemPrompt instructs the summarizing model to act as a context
// checkpoint writer rather than continue the conversation. It is BharatCode's
// own wording: produce only the structured checkpoint, never an answer.
const compactionSystemPrompt = `You are an anchored context-summarization assistant for a coding agent.

Your only job is to distill an in-progress engineering session into a compact,
durable checkpoint so the work can resume after older turns are dropped.

Strict rules:
- Do NOT continue the conversation, answer the user, or call any tool.
- Do NOT invent facts; record only what the transcript actually establishes.
- Prefer concrete specifics (file paths, identifiers, decisions, error text)
  over vague paraphrase.
- Be terse. Omit a section's bullets when nothing applies, but always keep the
  section headings so the structure stays stable.
- ONLY output the structured summary in exactly the requested Markdown format,
  with no preamble, no closing remarks, and nothing outside the template.`

// compactionTemplate is the exact Markdown checkpoint skeleton the summarizer
// must fill in. Keeping the heading set fixed lets downstream turns (and the
// iterative-update path) rely on a stable shape.
const compactionTemplate = `Summarize the session so far using EXACTLY this Markdown structure, keeping
every heading even when a section is empty:

## Goal
<the user's overall objective for this session>

## Constraints & Preferences
<requirements, conventions, and preferences the agent must honor>

## Progress
- Done: <what is finished and verified>
- In Progress: <what is partially done>
- Blocked: <what is stuck and why>

## Key Decisions
<important choices made and the reasoning behind them>

## Next Steps
<the concrete actions to take next, in order>

## Critical Context
<facts that must survive: invariants, gotchas, environment details>

## Relevant Files
<paths touched or central to the task, each with a one-line note>`

// compactionUpdateInstruction is prepended (with the prior summary) when an
// earlier checkpoint already exists, so the model refreshes it instead of
// starting from scratch.
const compactionUpdateInstruction = `A previous checkpoint already exists, shown below in <previous-summary> tags.
Produce an UPDATED checkpoint that supersedes it: preserve facts that are still
true, remove anything now stale or superseded, and merge in everything new from
the transcript. Output the full updated checkpoint, not a diff.`

// Compactor condenses a conversation history into a smaller equivalent that is
// cheaper to send to a provider. Implementations must be pure: they receive a
// copy of the history and return a new slice; they must not mutate the input.
type Compactor interface {
	// Compact returns a condensed form of history. The returned slice replaces
	// the in-memory history sent to the provider; it does not affect on-disk
	// session storage.
	Compact(ctx context.Context, history []message.Message) ([]message.Message, error)
}

// dropAndMarkCompactor is the default Compactor. It drops the older portion of
// the conversation and leaves a single synthetic marker message in its place,
// retaining a tail of recent messages verbatim. The marker preserves a short
// textual census of what was condensed so the model knows context was elided.
type dropAndMarkCompactor struct {
	// keepRecent is the number of trailing messages preserved verbatim.
	keepRecent int
}

// newDropAndMarkCompactor returns the default Compactor, retaining keepRecent
// trailing messages verbatim. A non-positive keepRecent is clamped to 1.
func newDropAndMarkCompactor(keepRecent int) dropAndMarkCompactor {
	if keepRecent < 1 {
		keepRecent = 1
	}
	return dropAndMarkCompactor{keepRecent: keepRecent}
}

// Compact drops all but the most recent keepRecent messages, replacing the
// dropped prefix with a single marker message summarizing the count. When the
// history already fits within keepRecent, it is returned unchanged.
func (c dropAndMarkCompactor) Compact(ctx context.Context, history []message.Message) ([]message.Message, error) {
	_ = ctx
	if len(history) <= c.keepRecent {
		return append([]message.Message(nil), history...), nil
	}
	dropped := history[:len(history)-c.keepRecent]
	tail := history[len(history)-c.keepRecent:]

	out := make([]message.Message, 0, len(tail)+1)
	out = append(out, message.Message{
		SessionID: sessionIDOf(history),
		Role:      message.RoleUser,
		Content: []message.ContentBlock{message.TextBlock{
			Text: fmt.Sprintf("%s %s", compactionSummaryMarker, summarizeDropped(dropped)),
		}},
		CreatedAt: history[0].CreatedAt,
	})
	out = append(out, tail...)
	return out, nil
}

// summaryProvider is the narrow slice of llm.Provider the llmSummaryCompactor
// needs: a single streaming call. It is an interface so tests can inject a fake
// provider that returns a fixed structured summary without a network round-trip.
type summaryProvider interface {
	Stream(ctx context.Context, req llm.Request) (<-chan llm.Event, error)
}

// llmSummaryCompactor condenses the dropped prefix of a conversation into a
// single structured checkpoint message produced by the provider, then keeps the
// recent tail verbatim. Unlike dropAndMarkCompactor, which leaves only a terse
// census, this Compactor asks the model to write a durable Markdown checkpoint
// so the agent retains the goal, decisions, and next steps across compaction.
//
// It supports iterative update: when the dropped prefix already contains a prior
// checkpoint (recognized by its marker), that checkpoint is passed back to the
// model in <previous-summary> tags with an instruction to refresh rather than
// rewrite it, so successive compactions accrete rather than forget.
type llmSummaryCompactor struct {
	// provider performs the single summarization call.
	provider summaryProvider
	// model is the model id used for the summarization call.
	model string
	// keepRecent is the number of trailing messages preserved verbatim.
	keepRecent int
}

// newLLMSummaryCompactor returns an llmSummaryCompactor that summarizes via
// provider using model, retaining keepRecent trailing messages verbatim. A
// non-positive keepRecent is clamped to 1.
func newLLMSummaryCompactor(provider summaryProvider, model string, keepRecent int) llmSummaryCompactor {
	if keepRecent < 1 {
		keepRecent = 1
	}
	return llmSummaryCompactor{provider: provider, model: model, keepRecent: keepRecent}
}

// Compact summarizes everything but the most recent keepRecent messages into a
// single structured checkpoint message, then appends the recent tail verbatim.
// When the history already fits within keepRecent there is nothing to drop and
// it is returned unchanged. The returned slice is always a fresh copy; the input
// is never mutated.
func (c llmSummaryCompactor) Compact(ctx context.Context, history []message.Message) ([]message.Message, error) {
	if len(history) <= c.keepRecent {
		return append([]message.Message(nil), history...), nil
	}
	dropped := history[:len(history)-c.keepRecent]
	tail := history[len(history)-c.keepRecent:]

	prior := extractPriorSummary(dropped)
	transcript := serializeForSummary(dropped)

	summary, err := c.summarize(ctx, transcript, prior)
	if err != nil {
		return nil, err
	}

	out := make([]message.Message, 0, len(tail)+1)
	out = append(out, message.Message{
		SessionID: sessionIDOf(history),
		Role:      message.RoleUser,
		Content: []message.ContentBlock{message.TextBlock{
			Text: fmt.Sprintf("%s\n\n%s", compactionSummaryMarker, summary),
		}},
		CreatedAt: history[0].CreatedAt,
	})
	out = append(out, tail...)
	return out, nil
}

// summarize calls the provider once with the compaction system prompt and the
// checkpoint template, returning the model's structured summary text. When prior
// is non-empty it is threaded through as a <previous-summary> block with the
// preserve/remove/merge instruction so the model updates the existing checkpoint
// rather than starting over.
func (c llmSummaryCompactor) summarize(ctx context.Context, transcript, prior string) (string, error) {
	var b strings.Builder
	if strings.TrimSpace(prior) != "" {
		b.WriteString(compactionUpdateInstruction)
		b.WriteString("\n\n<previous-summary>\n")
		b.WriteString(prior)
		b.WriteString("\n</previous-summary>\n\n")
	}
	b.WriteString(compactionTemplate)
	b.WriteString("\n\nHere is the conversation to summarize:\n\n")
	b.WriteString(transcript)

	req := llm.Request{
		Model:        c.model,
		SystemPrompt: compactionSystemPrompt,
		Messages: []message.Message{{
			Role:    message.RoleUser,
			Content: []message.ContentBlock{message.TextBlock{Text: b.String()}},
		}},
	}

	events, err := c.provider.Stream(ctx, req)
	if err != nil {
		return "", fmt.Errorf("requesting compaction summary: %w", err)
	}

	var text strings.Builder
	for {
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("reading compaction summary: %w", ctx.Err())
		case ev, ok := <-events:
			if !ok {
				out := strings.TrimSpace(text.String())
				if out == "" {
					return "", fmt.Errorf("compaction summary was empty")
				}
				return out, nil
			}
			switch e := ev.(type) {
			case llm.DeltaTextEvent:
				text.WriteString(e.Text)
			case llm.ErrorEvent:
				return "", fmt.Errorf("streaming compaction summary: %w", e.Err)
			}
		}
	}
}

// serializeForSummary renders dropped messages as role-tagged plain text so the
// summarizing model sees a readable transcript. Each tool result is truncated to
// toolResultSummaryLimit characters so a few large outputs cannot dominate the
// prefix. Empty messages are skipped.
func serializeForSummary(dropped []message.Message) string {
	var b strings.Builder
	for _, msg := range dropped {
		rendered := renderForSummary(msg)
		if rendered == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(rendered)
	}
	return b.String()
}

// renderForSummary renders a single message as a role-tagged block. Tool results
// (carried on user-role messages on the wire) are tagged [Tool result] and
// truncated; genuine user turns are tagged [User] and assistant turns
// [Assistant]. It returns an empty string when the message carries no
// summarizable text.
func renderForSummary(msg message.Message) string {
	var parts []string
	tag := "[User]"
	switch msg.Role {
	case message.RoleAssistant:
		tag = "[Assistant]"
	case message.RoleTool:
		tag = "[Tool result]"
	}
	for _, block := range msg.Content {
		switch b := block.(type) {
		case message.TextBlock:
			if strings.TrimSpace(b.Text) != "" {
				parts = append(parts, b.Text)
			}
		case message.ToolResultBlock:
			tag = "[Tool result]"
			parts = append(parts, truncateString(b.Content, toolResultSummaryLimit))
		case message.ToolUseBlock:
			parts = append(parts, fmt.Sprintf("(called tool %q with %s)", b.Name, string(b.Input)))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return tag + " " + strings.Join(parts, "\n")
}

// truncateString returns s capped at limit characters, appending an elision
// marker when it was shortened. A non-positive limit returns s unchanged.
func truncateString(s string, limit int) string {
	if limit <= 0 || len(s) <= limit {
		return s
	}
	return s[:limit] + "... [truncated]"
}

// extractPriorSummary returns the text of the most recent prior checkpoint found
// in dropped, identified by the compaction marker, with the marker stripped. It
// returns an empty string when no prior checkpoint is present, which signals the
// first (non-iterative) compaction.
func extractPriorSummary(dropped []message.Message) string {
	for i := len(dropped) - 1; i >= 0; i-- {
		text := textContent(dropped[i])
		if strings.Contains(text, compactionSummaryMarker) {
			stripped := strings.TrimSpace(strings.Replace(text, compactionSummaryMarker, "", 1))
			return stripped
		}
	}
	return ""
}

// summarizeDropped renders a terse, deterministic census of the dropped
// messages so the marker carries some signal about the elided context.
func summarizeDropped(dropped []message.Message) string {
	var users, assistants, tools int
	for _, msg := range dropped {
		switch msg.Role {
		case message.RoleUser:
			if hasToolResult(msg) {
				tools++
			} else {
				users++
			}
		case message.RoleAssistant:
			assistants++
		}
	}
	return fmt.Sprintf(
		"%d earlier messages condensed (%d user, %d assistant, %d tool result).",
		len(dropped), users, assistants, tools,
	)
}

func hasToolResult(msg message.Message) bool {
	for _, block := range msg.Content {
		if _, ok := block.(message.ToolResultBlock); ok {
			return true
		}
	}
	return false
}

func sessionIDOf(history []message.Message) string {
	for _, msg := range history {
		if msg.SessionID != "" {
			return msg.SessionID
		}
	}
	return ""
}

// latestUserIndex returns the index of the most recent message whose role is
// user and that does not carry a tool result, or -1 when none exists. Tool
// results are user-role on the wire but are not genuine user turns, so they are
// excluded to find the real prompt.
func latestUserIndex(history []message.Message) int {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == message.RoleUser && !hasToolResult(history[i]) {
			return i
		}
	}
	return -1
}

// containsMessage reports whether want appears in history by value equality of
// its serialized content and role. It is used to enforce the preserve-latest
// invariant without relying on pointer identity.
func containsMessage(history []message.Message, want message.Message) bool {
	wantText := strings.TrimSpace(textContent(want))
	for _, msg := range history {
		if msg.Role != want.Role {
			continue
		}
		if strings.TrimSpace(textContent(msg)) == wantText && wantText != "" {
			return true
		}
	}
	return false
}

func textContent(msg message.Message) string {
	var b strings.Builder
	for _, block := range msg.Content {
		if t, ok := block.(message.TextBlock); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String()
}

func truncateForContext(messages []message.Message, contextWindow int) []message.Message {
	if contextWindow <= 0 {
		return append([]message.Message(nil), messages...)
	}
	limit := messageBudget(contextWindow)
	if len(messages) <= 2 {
		return append([]message.Message(nil), messages...)
	}

	latestUser := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == message.RoleUser {
			latestUser = i
			break
		}
	}

	outReverse := make([]message.Message, 0, len(messages))
	total := 0
	for i := len(messages) - 1; i >= 0; i-- {
		tokens := estimateMessageTokens(messages[i])
		keep := total+tokens <= limit || i == latestUser
		if keep {
			outReverse = append(outReverse, messages[i])
			total += tokens
		}
	}
	out := make([]message.Message, 0, len(outReverse))
	for i := len(outReverse) - 1; i >= 0; i-- {
		out = append(out, outReverse[i])
	}
	return out
}

// messageBudget returns the token budget available for conversation messages
// given a context window, reserving headroom for the model's response. It
// mirrors the historical truncateForContext math: subtract the reserved
// response tokens, but fall back to the full window when the reservation would
// leave an implausibly small budget (a sign of a tiny, likely test, window).
func messageBudget(contextWindow int) int {
	limit := contextWindow - reservedResponseTokens
	if limit < 1024 {
		limit = contextWindow
	}
	return limit
}

// fitBudget returns the token budget available for conversation messages once
// both the reserved response headroom and the system prompt (which the provider
// sends alongside the messages but outside the history) are accounted for. It
// is used by the automatic-compaction path to decide whether a history fits the
// window.
//
// It mirrors the escape hatch in messageBudget: when reserving response headroom
// on top of the system prompt would leave an implausibly small budget (a sign
// the prompt is large relative to a modest window, common with smaller models),
// the response reservation is dropped so legitimately small turns are not
// declared a permanent overflow. The returned budget is only non-positive when
// the system prompt alone meets or exceeds the full context window.
func fitBudget(contextWindow int, systemPrompt string) int {
	promptTokens := estimateTextTokens(systemPrompt)
	limit := messageBudget(contextWindow) - promptTokens
	if limit < 1024 {
		// Dropping the response reservation recovers room the prompt would
		// otherwise have eaten via the reserved headroom alone.
		limit = contextWindow - promptTokens
	}
	return limit
}

// historyTokens estimates the total tokens a history occupies on the wire.
func historyTokens(messages []message.Message) int {
	total := 0
	for _, msg := range messages {
		total += estimateMessageTokens(msg)
	}
	return total
}

// fitsBudget reports whether messages fit within budget tokens. A non-positive
// budget never fits a non-empty history.
func fitsBudget(messages []message.Message, budget int) bool {
	if len(messages) == 0 {
		return true
	}
	return historyTokens(messages) <= budget
}

func estimateMessageTokens(msg message.Message) int {
	data, err := json.Marshal(msg.Content)
	if err != nil {
		return 256
	}
	n := len(data) / 4
	if n < 1 {
		return 1
	}
	return n
}

// estimateTextTokens estimates the tokens occupied by a raw text string, using
// the same ~4-bytes-per-token heuristic as estimateMessageTokens. An empty
// string costs zero tokens.
func estimateTextTokens(s string) int {
	if s == "" {
		return 0
	}
	n := len(s) / 4
	if n < 1 {
		return 1
	}
	return n
}
