package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/session"
	"github.com/stretchr/testify/require"
)

// seedExportSession creates a persisted session with a user prompt and an
// assistant reply so an export has real, multi-turn content to render.
func seedExportSession(t *testing.T, repo *session.Repo, userText, assistantText string) string {
	t.Helper()
	s := &session.Session{Title: "Export thread", Model: "fake-model", Agent: "coder"}
	require.NoError(t, repo.Create(context.Background(), s))
	require.NoError(t, repo.AppendMessage(context.Background(), s.ID, message.Message{
		Role:    message.RoleUser,
		Content: []message.ContentBlock{message.TextBlock{Text: userText}},
	}))
	require.NoError(t, repo.AppendMessage(context.Background(), s.ID, message.Message{
		Role:    message.RoleAssistant,
		Content: []message.ContentBlock{message.TextBlock{Text: assistantText}},
	}))
	return s.ID
}

// TestSlashExport_WritesMarkdownTranscript is the /export contract test: the
// command renders the active session's transcript to a Markdown file in the
// workspace, the file contains the real message content, and the confirmation
// dialog surfaces the written path.
func TestSlashExport_WritesMarkdownTranscript(t *testing.T) {
	provider := &scriptedProvider{}
	h := newAgentHarness(t, provider)
	m := h.model

	// Write into a temp workspace rather than the test's cwd.
	workspace := t.TempDir()
	m.exportDir = workspace

	id := seedExportSession(t, h.repo, "explain the bug", "the index is off by one")
	m.sessionID = id
	m.sessionPersisted = true

	h.submitSlash(t, "/export")

	require.True(t, m.dialogs.Contains("export"), "/export must surface a confirmation dialog")
	require.False(t, m.dialogs.Contains("error"), "a successful export must not raise an error dialog")

	expectedPath := filepath.Join(workspace, "bharatcode-session-"+shortSessionID(id)+".md")
	body := plainText(m.dialogs.Render(200))
	require.Contains(t, body, "Exported session", "confirmation must name the export")
	require.Contains(t, body, shortSessionID(id), "confirmation must reference the session in the path")

	// The file was actually written into the workspace.
	require.FileExists(t, expectedPath)
	data, err := os.ReadFile(expectedPath)
	require.NoError(t, err)
	content := string(data)
	require.Contains(t, content, "explain the bug", "exported file must contain the user message")
	require.Contains(t, content, "the index is off by one", "exported file must contain the assistant message")
	// It is Markdown, not HTML.
	require.True(t, strings.HasPrefix(content, "# "), "Markdown export must begin with a heading")
}

// TestSlashExport_HTMLFormat asserts "/export html" writes an HTML file with the
// message content and an .html extension.
func TestSlashExport_HTMLFormat(t *testing.T) {
	provider := &scriptedProvider{}
	h := newAgentHarness(t, provider)
	m := h.model

	workspace := t.TempDir()
	m.exportDir = workspace

	id := seedExportSession(t, h.repo, "render this please", "here is the rendered output")
	m.sessionID = id
	m.sessionPersisted = true

	h.submitSlash(t, "/export html")

	require.True(t, m.dialogs.Contains("export"))
	require.False(t, m.dialogs.Contains("error"))

	expectedPath := filepath.Join(workspace, "bharatcode-session-"+shortSessionID(id)+".html")
	require.FileExists(t, expectedPath)
	data, err := os.ReadFile(expectedPath)
	require.NoError(t, err)
	content := string(data)
	require.Contains(t, content, "<!DOCTYPE html>", "HTML export must be an HTML document")
	require.Contains(t, content, "render this please", "HTML export must contain the user message")
	require.Contains(t, content, "here is the rendered output", "HTML export must contain the assistant message")
}

// TestSlashExport_NoSession_ShowsPlaceholder asserts /export is a no-op with an
// explanatory dialog before any session has been persisted, and writes nothing.
func TestSlashExport_NoSession_ShowsPlaceholder(t *testing.T) {
	provider := &scriptedProvider{}
	h := newAgentHarness(t, provider)
	m := h.model

	workspace := t.TempDir()
	m.exportDir = workspace

	require.False(t, m.sessionPersisted)
	h.submitSlash(t, "/export")

	require.True(t, m.dialogs.Contains("export"), "/export must surface a guard dialog with no session")
	require.Contains(t, plainText(m.dialogs.Render(200)), "No active session")

	entries, err := os.ReadDir(workspace)
	require.NoError(t, err)
	require.Empty(t, entries, "no file must be written when there is no session to export")
}

// TestSlashExport_UnknownFormat_ShowsError asserts an unknown format argument is
// rejected with a helpful dialog and writes nothing.
func TestSlashExport_UnknownFormat_ShowsError(t *testing.T) {
	provider := &scriptedProvider{}
	h := newAgentHarness(t, provider)
	m := h.model

	workspace := t.TempDir()
	m.exportDir = workspace

	id := seedExportSession(t, h.repo, "hi", "hello")
	m.sessionID = id
	m.sessionPersisted = true

	h.submitSlash(t, "/export pdf")

	require.True(t, m.dialogs.Contains("export"))
	require.Contains(t, plainText(m.dialogs.Render(200)), "unknown export format")

	entries, err := os.ReadDir(workspace)
	require.NoError(t, err)
	require.Empty(t, entries, "an unknown format must not write a file")
}

// TestSlashShare_WritesMarkdownTranscript asserts /share is a working alias that
// writes the Markdown transcript to the workspace.
func TestSlashShare_WritesMarkdownTranscript(t *testing.T) {
	provider := &scriptedProvider{}
	h := newAgentHarness(t, provider)
	m := h.model

	workspace := t.TempDir()
	m.exportDir = workspace

	id := seedExportSession(t, h.repo, "share this thread", "shared content here")
	m.sessionID = id
	m.sessionPersisted = true

	h.submitSlash(t, "/share")

	require.True(t, m.dialogs.Contains("export"))
	expectedPath := filepath.Join(workspace, "bharatcode-session-"+shortSessionID(id)+".md")
	require.FileExists(t, expectedPath)
	data, err := os.ReadFile(expectedPath)
	require.NoError(t, err)
	require.Contains(t, string(data), "share this thread")
}
