package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/recipe"
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

// TestSlashSessions_TypeToFilterNarrowsAndRestores asserts the session picker
// supports live type-to-filter: typing narrows the visible rows by title, the
// cursor is bounded to the filtered set, and enter restores the matching
// session. Backspace widens the filter again.
func TestSlashSessions_TypeToFilterNarrowsAndRestores(t *testing.T) {
	provider := &scriptedProvider{}
	h := newAgentHarness(t, provider)
	m := h.model

	// Three sessions with distinct, searchable titles.
	_ = seedSession(t, h.repo, "Parser refactor", "fix the parser")
	bumpID := seedSession(t, h.repo, "Bump version", "release prep")
	_ = seedSession(t, h.repo, "Parser cleanup", "tidy the parser")

	h.submitSlash(t, "/sessions")
	require.True(t, m.dialogs.Contains("sessions"), "session picker must open")
	require.Len(t, m.sessionCandidates, 3, "all seeded sessions must be listed")

	// Type "bump" — only the single "Bump version" row should remain visible.
	for _, ch := range "bump" {
		_, _ = m.Update(keyText(string(ch)))
	}
	require.Equal(t, "bump", m.sessionFilter, "typed runes must extend the filter query")
	visible := m.visibleSessions()
	require.Len(t, visible, 1, "filter must narrow to the single matching session")
	require.Equal(t, bumpID, visible[0].ID, "the matching row must be the Bump session")

	body := plainText(m.dialogs.Render(200))
	require.Contains(t, body, "Filter: bump", "the active filter must be echoed in the picker")
	require.Contains(t, body, "Bump version", "the matching session must remain visible")
	require.NotContains(t, body, "Parser refactor", "non-matching sessions must be filtered out")

	// Backspace once widens "bump" -> "bum"; still one match here.
	_, _ = m.Update(keySpecial("backspace", tea.KeyBackspace))
	require.Equal(t, "bum", m.sessionFilter, "backspace must trim the filter query")

	// Enter restores the (single visible) filtered session.
	_, cmd := m.Update(keySpecial("enter", tea.KeyEnter))
	h.run(t, cmd)

	require.Equal(t, bumpID, m.sessionID, "enter must restore the filtered session")
	require.Equal(t, "", m.sessionFilter, "the filter must reset once a session is restored")
}

// TestSlashSessions_FuzzyFilterMatchesSubsequence asserts the picker filter
// accepts a scattered subsequence (typing "psr" finds "Parser refactor"), and
// that a substring match ranks ahead of a subsequence-only match regardless of
// recency order, mirroring the @-file picker's fuzzy ranking.
func TestSlashSessions_FuzzyFilterMatchesSubsequence(t *testing.T) {
	provider := &scriptedProvider{}
	h := newAgentHarness(t, provider)
	m := h.model

	// "Parser refactor" matches "psr" only as a subsequence; "psr tool" (seeded
	// last, so newest) contains "psr" as a substring and must rank first.
	refactorID := seedSession(t, h.repo, "Parser refactor", "fix the parser")
	_ = seedSession(t, h.repo, "Unrelated work", "nothing here")
	reloadID := seedSession(t, h.repo, "psr tool", "restart it")

	h.submitSlash(t, "/sessions")
	require.True(t, m.dialogs.Contains("sessions"), "session picker must open")

	for _, ch := range "psr" {
		_, _ = m.Update(keyText(string(ch)))
	}
	require.Equal(t, "psr", m.sessionFilter)

	visible := m.visibleSessions()
	require.Len(t, visible, 2, "both the substring and subsequence rows must match")
	require.Equal(t, reloadID, visible[0].ID, "the substring match must rank first")
	require.Equal(t, refactorID, visible[1].ID, "the subsequence-only match must follow")
}

// TestSlashSessions_TitlePrefixRanksAheadOfSubstring asserts that a session
// whose title begins with the query ranks ahead of an older session that merely
// contains the query mid-string, mirroring the @-file picker's base-name-prefix
// tier. Typing the start of a name should surface that session first even when a
// stale match is newer.
func TestSlashSessions_TitlePrefixRanksAheadOfSubstring(t *testing.T) {
	provider := &scriptedProvider{}
	h := newAgentHarness(t, provider)
	m := h.model

	// "Parser refactor" begins with "par"; "Compare parsers" only contains it
	// mid-string and is seeded last (so newer). The prefix match must still win.
	prefixID := seedSession(t, h.repo, "Parser refactor", "fix the parser")
	_ = seedSession(t, h.repo, "Unrelated work", "nothing here")
	substrID := seedSession(t, h.repo, "Compare parsers", "diff them")

	h.submitSlash(t, "/sessions")
	require.True(t, m.dialogs.Contains("sessions"), "session picker must open")

	for _, ch := range "par" {
		_, _ = m.Update(keyText(string(ch)))
	}
	require.Equal(t, "par", m.sessionFilter)

	visible := m.visibleSessions()
	require.Len(t, visible, 2, "both the prefix and substring rows must match")
	require.Equal(t, prefixID, visible[0].ID, "the title-prefix match must rank first")
	require.Equal(t, substrID, visible[1].ID, "the mid-string substring match must follow")
}

// TestHighlightSessionMatch_AccentsMatchedRunes asserts the session-title
// highlighter emphasizes exactly the runes a filter query matched and leaves the
// rest in the default color, mirroring the @-file and slash menus: the matched
// substring is wrapped in the accent style, the unmatched remainder is not, and
// the visible text round-trips unchanged once the styling is stripped.
func TestHighlightSessionMatch_AccentsMatchedRunes(t *testing.T) {
	t.Parallel()

	m := newSizedModel(t)

	// A contiguous, case-insensitive match: "par" lights the first three runes of
	// "Parser refactor". The matched run is accent-styled; the tail is plain.
	got := m.highlightSessionMatch("Parser refactor", "par")
	require.Equal(t, "Parser refactor", stripANSI(got), "highlighting must not alter the visible text")
	require.Contains(t, got, m.theme.Accent.Render("Par"), "the matched runes must be accent-styled")
	require.NotContains(t, got, m.theme.Accent.Render("ser refactor"), "the unmatched tail must stay in the default color")

	// An empty query and a no-match query both return the title byte-for-byte, so
	// no styling is added where it would not explain a result.
	require.Equal(t, "Parser refactor", m.highlightSessionMatch("Parser refactor", ""))
	require.Equal(t, "Parser refactor", m.highlightSessionMatch("Parser refactor", "zzz"))
}

// TestSlashSessions_FilterHighlightsMatchInBody asserts the open picker accents
// the matched runes of a session's title once a filter is active, so the body
// carries the accent-styled match while the surrounding row text stays plain.
func TestSlashSessions_FilterHighlightsMatchInBody(t *testing.T) {
	provider := &scriptedProvider{}
	h := newAgentHarness(t, provider)
	m := h.model

	_ = seedSession(t, h.repo, "Parser refactor", "fix the parser")

	h.submitSlash(t, "/sessions")
	require.True(t, m.dialogs.Contains("sessions"), "session picker must open")

	for _, ch := range "par" {
		_, _ = m.Update(keyText(string(ch)))
	}
	require.Equal(t, "par", m.sessionFilter)

	body := m.dialogs.Render(200)
	require.Contains(t, plainText(body), "Parser refactor", "the title's visible text must survive highlighting")
	require.Contains(t, body, m.theme.Accent.Render("Par"), "the matched runes must be accent-styled in the picker body")
}

// TestSlashSessions_HomeEndJumpToEnds asserts the session picker's Home/End
// bindings jump the cursor to the first and last visible rows, mirroring the
// chat's Home/End (oldest/newest) navigation, and that they are bounded so a
// repeated press cannot walk the cursor out of range.
func TestSlashSessions_HomeEndJumpToEnds(t *testing.T) {
	provider := &scriptedProvider{}
	h := newAgentHarness(t, provider)
	m := h.model

	for _, title := range []string{"alpha", "bravo", "charlie", "delta"} {
		_ = seedSession(t, h.repo, title, "work on "+title)
	}

	h.submitSlash(t, "/sessions")
	require.True(t, m.dialogs.Contains("sessions"), "session picker must open")
	last := len(m.visibleSessions()) - 1
	require.Greater(t, last, 0, "need several sessions to navigate")

	// Move off the first row, then End jumps to the last row.
	_, _ = m.Update(keySpecial("down", tea.KeyDown))
	require.Equal(t, 1, m.sessionCursor)
	_, _ = m.Update(keySpecial("end", tea.KeyEnd))
	require.Equal(t, last, m.sessionCursor, "End must jump to the last row")

	// A second End is a bounded no-op (never past the last row).
	_, _ = m.Update(keySpecial("end", tea.KeyEnd))
	require.Equal(t, last, m.sessionCursor, "End must not walk past the last row")

	// Home jumps back to the first row, and a repeat stays put.
	_, _ = m.Update(keySpecial("home", tea.KeyHome))
	require.Equal(t, 0, m.sessionCursor, "Home must jump to the first row")
	_, _ = m.Update(keySpecial("home", tea.KeyHome))
	require.Equal(t, 0, m.sessionCursor, "Home must not move below the first row")
}

// TestRelativeTime_CoarsensWithGap asserts the session-switcher last-active
// label coarsens granularity as the gap widens and never reads as a negative or
// empty age for a fresh or future timestamp.
func TestRelativeTime_CoarsensWithGap(t *testing.T) {
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		then time.Time
		want string
	}{
		{"zero timestamp", time.Time{}, "just now"},
		{"future timestamp", now.Add(time.Hour), "just now"},
		{"seconds ago", now.Add(-30 * time.Second), "just now"},
		{"minutes ago", now.Add(-5 * time.Minute), "5m ago"},
		{"hours ago", now.Add(-3 * time.Hour), "3h ago"},
		{"days ago", now.Add(-2 * 24 * time.Hour), "2d ago"},
		{"weeks ago", now.Add(-3 * 7 * 24 * time.Hour), "3w ago"},
		{"just under a month stays weeks", now.Add(-29 * 24 * time.Hour), "4w ago"},
		{"months ago", now.Add(-60 * 24 * time.Hour), "2mo ago"},
		{"just under a year stays months", now.Add(-360 * 24 * time.Hour), "12mo ago"},
		{"years ago", now.Add(-2 * 365 * 24 * time.Hour), "2y ago"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, relativeTime(tc.then, now))
		})
	}
}

// TestSlashSessions_ShowsLastActiveColumn asserts the session picker body
// includes a relative last-active label for each row, so a returning user can
// see at a glance which session is the most recent.
func TestSlashSessions_ShowsLastActiveColumn(t *testing.T) {
	provider := &scriptedProvider{}
	h := newAgentHarness(t, provider)
	m := h.model

	_ = seedSession(t, h.repo, "Recent work", "fix the parser")

	h.submitSlash(t, "/sessions")
	require.True(t, m.dialogs.Contains("sessions"), "session picker must open")

	body := plainText(m.dialogs.Render(200))
	require.Contains(t, body, "just now", "a freshly-seeded session row must carry a relative last-active label")
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

// TestSlashPlanAndApprove_TogglesPlanModeOnLiveLoop is the /plan and /approve
// contract test: /plan enables plan mode on the live agent loop and /approve
// exits it, with a confirmation dialog at each step.
func TestSlashPlanAndApprove_TogglesPlanModeOnLiveLoop(t *testing.T) {
	provider := &scriptedProvider{}
	h := newAgentHarness(t, provider)
	m := h.model

	require.False(t, m.deps.Agent.PlanMode(), "plan mode is off before /plan")

	h.submitSlash(t, "/plan")
	require.True(t, m.deps.Agent.PlanMode(), "/plan must enable plan mode on the live loop")
	require.True(t, m.dialogs.Contains("plan"))

	// Dismiss the confirmation dialog (enter) before issuing the next command,
	// since an open dialog intercepts keypresses.
	m.Update(keySpecial("enter", tea.KeyEnter))
	require.Equal(t, 0, m.dialogs.Len(), "the plan confirmation must be dismissable")

	h.submitSlash(t, "/approve")
	require.False(t, m.deps.Agent.PlanMode(), "/approve must exit plan mode")
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

// TestSlashRegistryPrompt_ExpandsPositionalArgs asserts that pi-style
// positional placeholders ($1, $2, $@) in a registered prompt are expanded
// from the slash argument line and the result reaches the agent loop.
func TestSlashRegistryPrompt_ExpandsPositionalArgs(t *testing.T) {
	provider := &scriptedProvider{scripts: [][]llm.Event{
		{
			llm.DeltaTextEvent{Text: "on it"},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 1, OutputTokens: 1}},
		},
	}}
	h := newAgentHarness(t, provider)
	m := h.model

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "review.md"),
		[]byte("Review $1 for $2 (all: $@)"), 0o644))
	reg, err := config.LoadPromptRegistry(dir)
	require.NoError(t, err)
	m.deps.Prompts = reg

	h.submitSlash(t, "/review main.go races")
	h.drain(t, func() bool { return !m.running })

	require.False(t, m.dialogs.Contains("error"), "a registered prompt must not raise the unknown-command dialog")

	msgs, err := h.repo.Messages(context.Background(), m.sessionID)
	require.NoError(t, err)
	require.Equal(t, "Review main.go for races (all: main.go races)", firstUserText(msgs),
		"positional placeholders must be expanded from the slash argument line before reaching the agent loop")
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

// TestDynamicSlashNames_RecipesThenPrompts asserts dynamicSlashNames gathers the
// recipe and custom-prompt names from the registries as leading-slash commands,
// recipes first and prompts after, matching the order /help prints them, so Tab
// completion and the hint dropdown list user commands the same way the help dump
// does. Nil registries contribute nothing.
func TestDynamicSlashNames_RecipesThenPrompts(t *testing.T) {
	t.Parallel()

	require.Nil(t, dynamicSlashNames(Dependencies{}),
		"with no registries there are no dynamic commands")

	recipeDir := t.TempDir()
	writeRecipeFile(t, recipeDir, "deploy", recipe.Recipe{Title: "Deploy", Prompt: "ship it"})
	recipes, err := recipe.NewRegistry(recipeDir)
	require.NoError(t, err)

	promptDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(promptDir, "triage.md"), []byte("Triage {{input}}"), 0o644))
	prompts, err := config.LoadPromptRegistry(promptDir)
	require.NoError(t, err)

	require.Equal(t, []string{"/deploy", "/triage"},
		dynamicSlashNames(Dependencies{Recipes: recipes, Prompts: prompts}),
		"recipes are listed first, then custom prompts, each as a /name command")
}

// TestDynamicSlashDescriptions_RecipeTitleAndPromptDescription asserts the gloss
// map picks a recipe's title and a custom prompt's frontmatter description, keyed
// by /name, and falls back to the prompt template's first line when no description
// is declared. Commands with no usable text are omitted so the completion menu
// never appends a bare "— ". Nil registries contribute nothing.
func TestDynamicSlashDescriptions_RecipeTitleAndPromptDescription(t *testing.T) {
	t.Parallel()

	require.Empty(t, dynamicSlashDescriptions(Dependencies{}),
		"with no registries there are no descriptions")

	recipeDir := t.TempDir()
	writeRecipeFile(t, recipeDir, "deploy", recipe.Recipe{Title: "Deploy the app", Prompt: "ship it"})
	recipes, err := recipe.NewRegistry(recipeDir)
	require.NoError(t, err)

	promptDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(promptDir, "triage.md"),
		[]byte("---\ndescription: sort the open issues\n---\nTriage {{input}}"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(promptDir, "note.md"),
		[]byte("Jot a quick {{input}}"), 0o644))
	prompts, err := config.LoadPromptRegistry(promptDir)
	require.NoError(t, err)

	got := dynamicSlashDescriptions(Dependencies{Recipes: recipes, Prompts: prompts})
	require.Equal(t, "Deploy the app", got["/deploy"], "a recipe is described by its title")
	require.Equal(t, "sort the open issues", got["/triage"],
		"a custom prompt is described by its frontmatter description")
	require.Equal(t, "Jot a quick {{input}}", got["/note"],
		"with no description the prompt's first template line is used")
}

// writeRecipeFile writes a recipe JSON file to dir and returns its path.
func writeRecipeFile(t *testing.T, dir, name string, r recipe.Recipe) {
	t.Helper()
	data, err := json.Marshal(r)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, name+".recipe.json"), data, 0o644))
}

// seedRecipeRegistry registers a recipe registry on the model from a temp dir.
func seedRecipeRegistry(t *testing.T, m *model, dir string) *recipe.Registry {
	t.Helper()
	reg, err := recipe.NewRegistry(dir)
	require.NoError(t, err)
	m.deps.Recipes = reg
	m.recipeCollector = nil
	return reg
}

// TestSlashRegistryRecipe_NoParams_RendersAndSubmits is the core recipe contract
// test: a "/<name>" command whose recipe has no user_prompt parameters must
// render the prompt immediately and submit it to the agent without opening any
// dialog. The rendered text must be persisted as the user message.
func TestSlashRegistryRecipe_NoParams_RendersAndSubmits(t *testing.T) {
	provider := &scriptedProvider{scripts: [][]llm.Event{
		{
			llm.DeltaTextEvent{Text: "done"},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 1, OutputTokens: 1}},
		},
	}}
	h := newAgentHarness(t, provider)
	m := h.model

	dir := t.TempDir()
	writeRecipeFile(t, dir, "greet", recipe.Recipe{
		Title:  "Greet",
		Prompt: "Say hello to the team.",
	})
	seedRecipeRegistry(t, m, dir)

	h.submitSlash(t, "/greet")
	h.drain(t, func() bool { return !m.running })

	require.False(t, m.dialogs.Contains("error"), "a registered recipe must not raise the error dialog")
	msgs, err := h.repo.Messages(context.Background(), m.sessionID)
	require.NoError(t, err)
	require.Equal(t, "Say hello to the team.", firstUserText(msgs),
		"the rendered recipe must be submitted to the agent loop as the user message")
}

// TestSlashRegistryRecipe_ArgsPrePopulatesInput asserts that trailing args after
// the recipe name are used as the "input" substitution and also pre-populate a
// single user_prompt parameter, so a one-param recipe works without a dialog.
func TestSlashRegistryRecipe_ArgsPrePopulatesInput(t *testing.T) {
	provider := &scriptedProvider{scripts: [][]llm.Event{
		{
			llm.DeltaTextEvent{Text: "done"},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 1, OutputTokens: 1}},
		},
	}}
	h := newAgentHarness(t, provider)
	m := h.model

	dir := t.TempDir()
	writeRecipeFile(t, dir, "review", recipe.Recipe{
		Title:  "Code review",
		Prompt: "Review the following file: {{target}}",
		Parameters: []recipe.Parameter{
			{
				Name:        "target",
				Type:        recipe.ParamTypeString,
				Requirement: recipe.RequirementUserPrompt,
				Description: "File to review",
			},
		},
	})
	seedRecipeRegistry(t, m, dir)

	// Pass the target as trailing args — no dialog should open because a single
	// user_prompt param is pre-populated from the args.
	h.submitSlash(t, "/review main.go")
	h.drain(t, func() bool { return !m.running })

	require.False(t, m.dialogs.Contains("error"), "recipe with pre-populated param must not raise error")
	require.Nil(t, m.recipeCollector, "collector must be cleared after completion")

	msgs, err := h.repo.Messages(context.Background(), m.sessionID)
	require.NoError(t, err)
	require.Equal(t, "Review the following file: main.go", firstUserText(msgs),
		"args must pre-populate the single user_prompt parameter without a dialog")
}

// TestSlashRegistryRecipe_UserPromptParam_CollectsViaDialog is the interactive
// parameter collection contract test: when a recipe has a user_prompt parameter
// that cannot be pre-populated from args (two params), the TUI must push a
// dialog, collect the value on enter, and then submit the rendered recipe.
func TestSlashRegistryRecipe_UserPromptParam_CollectsViaDialog(t *testing.T) {
	provider := &scriptedProvider{scripts: [][]llm.Event{
		{
			llm.DeltaTextEvent{Text: "done"},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 1, OutputTokens: 1}},
		},
	}}
	h := newAgentHarness(t, provider)
	m := h.model

	dir := t.TempDir()
	writeRecipeFile(t, dir, "test-gen", recipe.Recipe{
		Title:  "Generate tests",
		Prompt: "Write tests for {{package}} targeting {{coverage}}.",
		Parameters: []recipe.Parameter{
			{
				Name:        "package",
				Type:        recipe.ParamTypeString,
				Requirement: recipe.RequirementUserPrompt,
				Description: "Go package to test",
			},
			{
				Name:        "coverage",
				Type:        recipe.ParamTypeString,
				Requirement: recipe.RequirementUserPrompt,
				Description: "Coverage target",
				Default:     "80%",
			},
		},
	})
	seedRecipeRegistry(t, m, dir)

	// Invoke the recipe; two params → first dialog must open.
	h.submitSlash(t, "/test-gen")
	require.True(t, m.dialogs.Contains("recipe_param_package"),
		"recipe with user_prompt param must open a parameter dialog")
	require.NotNil(t, m.recipeCollector, "collector must be active while dialogs are open")
	require.False(t, m.running, "agent must not start until all params are collected")

	// Type the package name and press enter to submit.
	for _, ch := range "internal/config" {
		_, _ = m.Update(keyText(string(ch)))
	}
	_, _ = m.Update(keySpecial("enter", tea.KeyEnter))

	// The first dialog popped; the second (coverage) must now be open.
	require.True(t, m.dialogs.Contains("recipe_param_coverage"),
		"after submitting the first param the second param dialog must open")

	// Accept the default for coverage by pressing enter with an empty buffer.
	_, cmd := m.Update(keySpecial("enter", tea.KeyEnter))
	h.startBatch(t, cmd)
	h.drain(t, func() bool { return !m.running })

	require.Nil(t, m.recipeCollector, "collector must be cleared after completion")
	require.False(t, m.dialogs.Contains("error"), "completed recipe must not raise an error dialog")

	msgs, err := h.repo.Messages(context.Background(), m.sessionID)
	require.NoError(t, err)
	require.Equal(t,
		"Write tests for internal/config targeting 80%.",
		firstUserText(msgs),
		"rendered recipe with collected params must reach the agent loop")
}

// TestSlashRegistryRecipe_EscCancels asserts that pressing esc during parameter
// collection surfaces a cancellation dialog and does NOT start an agent run.
func TestSlashRegistryRecipe_EscCancels(t *testing.T) {
	provider := &scriptedProvider{}
	h := newAgentHarness(t, provider)
	m := h.model

	dir := t.TempDir()
	writeRecipeFile(t, dir, "regen", recipe.Recipe{
		Title:  "Regenerate",
		Prompt: "Regenerate {{target}}.",
		Parameters: []recipe.Parameter{
			{
				Name:        "target",
				Type:        recipe.ParamTypeString,
				Requirement: recipe.RequirementUserPrompt,
				Description: "What to regenerate",
			},
		},
	})
	seedRecipeRegistry(t, m, dir)

	// Open the recipe — one user_prompt param → dialog opens.
	h.submitSlash(t, "/regen")
	require.True(t, m.dialogs.Contains("recipe_param_target"),
		"user_prompt param must trigger a dialog")

	// Cancel with esc.
	_, _ = m.Update(keySpecial("esc", tea.KeyEsc))

	require.False(t, m.running, "esc during parameter collection must not start an agent run")
	require.Nil(t, m.recipeCollector, "collector must be cleared after cancellation")
	require.True(t, m.dialogs.Contains("recipe_cancelled"),
		"esc must surface the recipe_cancelled dialog")
	require.Equal(t, 0, provider.calls(), "no provider call must be made when recipe is cancelled")
}

// TestSlashRegistryRecipe_UnknownNameFallsToErrorDialog asserts that an unknown
// slash command that also has no matching recipe falls through to the
// unknown-command error dialog (both registries are checked before falling back).
func TestSlashRegistryRecipe_UnknownNameFallsToErrorDialog(t *testing.T) {
	provider := &scriptedProvider{}
	h := newAgentHarness(t, provider)
	m := h.model

	dir := t.TempDir()
	writeRecipeFile(t, dir, "greet", recipe.Recipe{
		Title:  "Greet",
		Prompt: "Say hello.",
	})
	seedRecipeRegistry(t, m, dir)

	h.submitSlash(t, "/no-such-recipe")
	require.True(t, m.dialogs.Contains("error"),
		"a command unknown to both prompt and recipe registries must raise the error dialog")
	require.Contains(t, m.dialogs.Render(200), "/no-such-recipe")
}

// TestSlashHelp_ListsRegisteredRecipes asserts that /help output includes
// registered recipe names alongside the built-in commands.
func TestSlashHelp_ListsRegisteredRecipes(t *testing.T) {
	m := newSizedModel(t)

	dir := t.TempDir()
	writeRecipeFile(t, dir, "daily-standup", recipe.Recipe{
		Title:  "Daily standup",
		Prompt: "Summarise progress.",
	})
	reg, err := recipe.NewRegistry(dir)
	require.NoError(t, err)
	m.deps.Recipes = reg

	// The help listing is rendered inside the chat viewport, which clamps from
	// the top when the content overflows. Give it a window tall enough to hold
	// the full built-in list plus the recipe so this test exercises the listing's
	// content rather than where the viewport happens to cut it.
	_, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 60})

	m.helpVisible = true
	out := plainText(m.renderMain())

	require.Contains(t, out, "/help", "built-in commands must still appear in /help")
	require.Contains(t, out, "/daily-standup", "registered recipe name must appear in /help output")
	require.Contains(t, out, "Daily standup", "registered recipe title must appear in /help output")
}

// TestSlashHelp_DocumentsEveryBuiltinCommand guards against the /help listing
// drifting out of sync with the completable command set: every command the user
// can Tab-complete must carry a documenting line in /help, so a command like
// /keys is never silently undiscoverable from the help dump. It mirrors
// TestSlashCommandsAllHaveDescriptions, which makes the same coverage guarantee
// for the inline slash-hint gloss.
func TestSlashHelp_DocumentsEveryBuiltinCommand(t *testing.T) {
	m := newSizedModel(t)

	lines := m.slashHelpLines()
	for _, cmd := range slashCommands {
		found := false
		for _, line := range lines {
			// Help lines read "/cmd [args] - description"; match the command token
			// at the line start so "/tab" does not spuriously satisfy "/tabs".
			if line == cmd || strings.HasPrefix(line, cmd+" ") {
				found = true
				break
			}
		}
		require.Truef(t, found, "completable command %s must be documented in /help", cmd)
	}
}

// TestSlashHelp_ListsCustomPromptsWithFrontmatter asserts that registered
// custom prompts appear in /help, documented by their frontmatter description
// and argument hint.
func TestSlashHelp_ListsCustomPromptsWithFrontmatter(t *testing.T) {
	m := newSizedModel(t)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "triage.md"),
		[]byte("---\ndescription: Triage a flaky test\nargument-hint: <test-name>\n---\nTriage {{input}} now."),
		0o644,
	))
	reg, err := config.LoadPromptRegistry(dir)
	require.NoError(t, err)
	m.deps.Prompts = reg

	m.helpVisible = true
	out := plainText(m.renderMain())

	require.Contains(t, out, "/triage", "registered prompt name must appear in /help output")
	require.Contains(t, out, "<test-name>", "prompt argument hint must appear in /help output")
	require.Contains(t, out, "Triage a flaky test", "prompt description must appear in /help output")
}
