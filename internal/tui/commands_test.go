package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/session"
	tea "github.com/charmbracelet/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

// indexOfSession returns the index of id within candidates, or -1.
func indexOfSession(candidates []session.Session, id string) int {
	for i, s := range candidates {
		if s.ID == id {
			return i
		}
	}
	return -1
}

// seedSession creates a persisted session with the given title and a single
// user message so the picker has a real, message-bearing row to restore.
func seedSession(t *testing.T, repo *session.Repo, title, userText string) string {
	t.Helper()
	s := &session.Session{Title: title, Model: "fake-model", Agent: "coder"}
	require.NoError(t, repo.Create(context.Background(), s))
	require.NoError(t, repo.AppendMessage(context.Background(), s.ID, message.Message{
		Role:    message.RoleUser,
		Content: []message.ContentBlock{message.TextBlock{Text: userText}},
	}))
	return s.ID
}

// TestSlashSessions_RestoresChosenSession is the /sessions contract test: the
// picker lists real sessions, arrow-key navigation moves the cursor, and enter
// switches m.sessionID to the chosen session and loads its transcript into the
// chat view.
func TestSlashSessions_RestoresChosenSession(t *testing.T) {
	provider := &scriptedProvider{}
	h := newAgentHarness(t, provider)
	m := h.model

	// Two distinct, real sessions with distinct transcripts.
	firstID := seedSession(t, h.repo, "First session", "fix the parser")
	_ = seedSession(t, h.repo, "Second session", "add a flag")

	h.submitSlash(t, "/sessions")
	require.True(t, m.dialogs.Contains("sessions"), "session picker must open")
	require.Len(t, m.sessionCandidates, 2, "both seeded sessions must be listed")

	// Locate the row for firstID (second-resolution timestamps make the list
	// order a tie, so select by id rather than by a fixed index) and move the
	// cursor there with real key events.
	target := indexOfSession(m.sessionCandidates, firstID)
	require.GreaterOrEqual(t, target, 0, "the first session must be in the picker")
	for m.sessionCursor < target {
		_, _ = m.Update(keySpecial("down", tea.KeyDown))
	}
	require.Equal(t, target, m.sessionCursor)

	_, cmd := m.Update(keySpecial("enter", tea.KeyEnter))
	h.run(t, cmd)

	require.Equal(t, firstID, m.sessionID, "enter must switch the active session to the chosen row")
	require.True(t, m.sessionPersisted, "restored session must be marked persisted")
	require.Equal(t, firstID, m.status.SessionID, "status bar must reflect the restored session")

	rendered := plainText(m.chat.Render(200))
	require.Contains(t, rendered, "fix the parser", "restored session's transcript must load into the chat")
	require.NotContains(t, rendered, "add a flag", "the unchosen session's transcript must not be shown")
}

// TestSlashFork_CreatesAndSwitchesToNewSession is the /fork contract test: it
// branches the active session into a new persisted session with its own id,
// switches to it, and copies the transcript.
func TestSlashFork_CreatesAndSwitchesToNewSession(t *testing.T) {
	provider := &scriptedProvider{scripts: [][]llm.Event{
		{
			llm.DeltaTextEvent{Text: "working"},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 1, OutputTokens: 1}},
		},
	}}
	h := newAgentHarness(t, provider)
	m := h.model

	// Establish a real, persisted session by running one prompt.
	h.submit(t, "remember this thread")
	h.drain(t, func() bool { return !m.running })
	original := m.sessionID
	require.True(t, m.sessionPersisted)

	h.submitSlash(t, "/fork")

	require.NotEqual(t, original, m.sessionID, "fork must switch to a new session id")
	require.True(t, m.dialogs.Contains("fork"), "fork must surface a confirmation dialog")

	// The new session is a distinct, real row.
	forked, err := h.repo.Get(context.Background(), m.sessionID)
	require.NoError(t, err)
	require.NotEqual(t, original, forked.ID)

	// The original prompt was carried into the fork's transcript.
	msgs, err := h.repo.Messages(context.Background(), forked.ID)
	require.NoError(t, err)
	require.Equal(t, "remember this thread", firstUserText(msgs), "fork must copy the source transcript")
}

// TestSlashCompact_ShrinksHistoryAndConfirms is the /compact contract test. It
// seeds a multi-turn session, runs /compact, and asserts two things:
//   - the confirmation surfaces in the chat, and
//   - the compaction seam actually fired: the next provider request carries a
//     SMALLER history than the full persisted transcript. The default compactor
//     keeps the last 2 messages plus a synthetic marker, so a transcript of
//     several messages must shrink. This proves the handler wired to the loop's
//     Compact method, not merely that it printed a string.
func TestSlashCompact_ShrinksHistoryAndConfirms(t *testing.T) {
	provider := &scriptedProvider{scripts: [][]llm.Event{
		{
			llm.DeltaTextEvent{Text: "post-compact reply"},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 1, OutputTokens: 1}},
		},
	}}
	h := newAgentHarness(t, provider)
	m := h.model

	// Seed a persisted session with a transcript well beyond the compactor's
	// keepRecent tail so compaction must drop messages.
	s := &session.Session{Title: "Long thread", Model: "fake-model", Agent: "coder"}
	require.NoError(t, h.repo.Create(context.Background(), s))
	for i := 0; i < 6; i++ {
		require.NoError(t, h.repo.AppendMessage(context.Background(), s.ID, message.Message{
			Role:    message.RoleUser,
			Content: []message.ContentBlock{message.TextBlock{Text: "user turn"}},
		}))
		require.NoError(t, h.repo.AppendMessage(context.Background(), s.ID, message.Message{
			Role:    message.RoleAssistant,
			Content: []message.ContentBlock{message.TextBlock{Text: "assistant turn"}},
		}))
	}
	persisted, err := h.repo.Messages(context.Background(), s.ID)
	require.NoError(t, err)
	require.Greater(t, len(persisted), 3, "the seeded transcript must exceed the compactor tail")

	m.sessionID = s.ID
	m.sessionPersisted = true

	h.submitSlash(t, "/compact")

	// The confirmation is surfaced in the chat (named deliverable).
	require.Contains(t, plainText(m.chat.Render(200)), "Context compacted",
		"/compact must surface a confirmation in the chat")
	require.False(t, m.dialogs.Contains("error"), "a successful compaction must not raise an error dialog")

	// The seam actually fired: the next turn's provider request must carry a
	// smaller history than the full persisted transcript.
	h.submit(t, "continue please")
	h.drain(t, func() bool { return !m.running })

	sent := provider.lastRequest().Messages
	require.NotEmpty(t, sent, "the post-compact turn must reach the provider")
	// persisted has 12 messages at compact time; the post-compact request must
	// carry far fewer (the compactor keeps a small tail plus a marker, then the
	// loop grafts only the one new prompt onto the snapshot).
	require.Less(t, len(sent), len(persisted),
		"compaction must shrink the history the agent sends to the provider")
	// The on-disk transcript is never mutated by compaction.
	after, err := h.repo.Messages(context.Background(), s.ID)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(after), len(persisted),
		"compaction must not delete persisted messages from the session")
}

// TestSlashCompact_NoSession_ShowsPlaceholder asserts /compact is a no-op with
// an explanatory dialog before any session has been persisted.
func TestSlashCompact_NoSession_ShowsPlaceholder(t *testing.T) {
	provider := &scriptedProvider{}
	h := newAgentHarness(t, provider)
	m := h.model

	require.False(t, m.sessionPersisted)
	h.submitSlash(t, "/compact")

	require.True(t, m.dialogs.Contains("compact"), "/compact must surface a guard dialog with no session")
	require.Contains(t, plainText(m.dialogs.Render(200)), "No active session")
	require.NotContains(t, plainText(m.chat.Render(200)), "Context compacted",
		"no confirmation must appear when there is nothing to compact")
}

// TestSlashStatus_ShowsModelSessionAndCount is the /status contract test: the
// panel must contain the model, the session id, and the message count.
func TestSlashStatus_ShowsModelSessionAndCount(t *testing.T) {
	provider := &scriptedProvider{scripts: [][]llm.Event{
		{
			llm.DeltaTextEvent{Text: "ok"},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 1, OutputTokens: 1}},
		},
	}}
	h := newAgentHarness(t, provider)
	m := h.model

	h.submit(t, "do the thing")
	h.drain(t, func() bool { return !m.running })

	h.submitSlash(t, "/status")
	require.True(t, m.dialogs.Contains("status"))
	body := plainText(m.dialogs.Render(200))

	require.Contains(t, body, "fake-model", "status must show the model")
	require.Contains(t, body, shortSessionID(m.sessionID), "status must show the session id")
	require.Contains(t, body, "Messages:", "status must show the message count label")
	// The run persisted at least one user and one assistant message.
	count := m.sessionMessageCount()
	require.GreaterOrEqual(t, count, 2, "persisted message count must be real")
}

// TestSlashDiff_RendersLatestEdit is the /diff contract test: the most recent
// edit tool call's before/after text must appear in the rendered diff.
func TestSlashDiff_RendersLatestEdit(t *testing.T) {
	provider := &scriptedProvider{}
	h := newAgentHarness(t, provider)
	m := h.model

	// Seed a persisted session whose transcript contains an edit tool call.
	s := &session.Session{Title: "Edited", Model: "fake-model", Agent: "coder"}
	require.NoError(t, h.repo.Create(context.Background(), s))
	require.NoError(t, h.repo.AppendMessage(context.Background(), s.ID, message.Message{
		Role:    message.RoleUser,
		Content: []message.ContentBlock{message.TextBlock{Text: "rename the func"}},
	}))
	require.NoError(t, h.repo.AppendMessage(context.Background(), s.ID, message.Message{
		Role: message.RoleAssistant,
		Content: []message.ContentBlock{
			message.TextBlock{Text: "Renaming now."},
			message.ToolUseBlock{
				ID:    "call-edit",
				Name:  "edit",
				Input: json.RawMessage(`{"path":"main.go","old_string":"func old() {}","new_string":"func renamed() {}"}`),
			},
		},
	}))

	// Activate that session.
	m.sessionID = s.ID
	m.sessionPersisted = true

	h.submitSlash(t, "/diff")
	require.True(t, m.dialogs.Contains("diff"))
	body := plainText(m.dialogs.Render(200))

	require.Contains(t, body, "main.go", "diff must name the edited file")
	require.Contains(t, body, "func old() {}", "diff must show the removed line")
	require.Contains(t, body, "func renamed() {}", "diff must show the added line")
}

// TestSlashDiff_NoEdit_ShowsPlaceholder asserts the placeholder is shown when
// the session has no edit/write/multiedit tool call.
func TestSlashDiff_NoEdit_ShowsPlaceholder(t *testing.T) {
	provider := &scriptedProvider{}
	h := newAgentHarness(t, provider)
	m := h.model

	id := seedSession(t, h.repo, "Plain", "just chatting")
	m.sessionID = id
	m.sessionPersisted = true

	h.submitSlash(t, "/diff")
	require.Contains(t, plainText(m.dialogs.Render(200)), "No edit diff is available yet")
}

// TestSlashRegistryPrompt_RendersAndSubmits is the prompt-registry contract
// test: a "/<name> args" line whose name is in the registry must render with
// args spliced into {{input}} and submit the expansion to the agent. The
// expanded text must reach the agent loop (assert on the persisted user
// message).
func TestSlashRegistryPrompt_RendersAndSubmits(t *testing.T) {
	provider := &scriptedProvider{scripts: [][]llm.Event{
		{
			llm.DeltaTextEvent{Text: "on it"},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 1, OutputTokens: 1}},
		},
	}}
	h := newAgentHarness(t, provider)
	m := h.model

	// Build a real registry from a temp dir with a {{input}}-bearing template.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "triage.md"), []byte("Triage this issue carefully: {{input}}"), 0o644))
	reg, err := config.LoadPromptRegistry(dir)
	require.NoError(t, err)
	m.deps.Prompts = reg

	h.submitSlash(t, "/triage flaky test in CI")
	h.drain(t, func() bool { return !m.running })

	require.False(t, m.dialogs.Contains("error"), "a registered prompt must not raise the unknown-command dialog")

	msgs, err := h.repo.Messages(context.Background(), m.sessionID)
	require.NoError(t, err)
	require.Equal(t, "Triage this issue carefully: flaky test in CI", firstUserText(msgs),
		"the rendered prompt (with args spliced into {{input}}) must reach the agent loop")
}

// TestSlashRegistryPrompt_UnknownFallsBackToErrorDialog asserts an unknown
// command that is neither built in nor registered raises the unknown-command
// dialog.
func TestSlashRegistryPrompt_UnknownFallsBackToErrorDialog(t *testing.T) {
	provider := &scriptedProvider{}
	h := newAgentHarness(t, provider)
	m := h.model

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "triage.md"), []byte("Triage {{input}}"), 0o644))
	reg, err := config.LoadPromptRegistry(dir)
	require.NoError(t, err)
	m.deps.Prompts = reg

	h.submitSlash(t, "/nonexistent do something")
	require.True(t, m.dialogs.Contains("error"), "an unregistered slash command must raise the unknown-command dialog")
	require.Contains(t, m.dialogs.Render(200), "/nonexistent")
}
