package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/session"
	"github.com/arbazkhan971/bharatcode/internal/tui/dialog"
	tea "github.com/charmbracelet/bubbletea/v2"
)

// exportFormat selects which renderer /export uses.
type exportFormat int

const (
	exportMarkdown exportFormat = iota
	exportHTML
)

// handleExport renders the current session's transcript to a file in the
// workspace and surfaces the written path in a confirmation dialog. The
// command accepts an optional format argument: "/export" and "/export md"
// write Markdown, while "/export html" writes HTML. "/share" is an alias that
// also defaults to Markdown. It is a no-op with an explanatory dialog when
// there is no persisted session to export.
func (m *model) handleExport(text string) (tea.Model, tea.Cmd) {
	format, err := parseExportFormat(text)
	if err != nil {
		m.dialogs.Push(&dialog.Text{DialogID: "export", Title: "Export", Body: err.Error(), Theme: m.theme})
		return m, nil
	}

	if !m.sessionPersisted {
		m.dialogs.Push(&dialog.Text{DialogID: "export", Title: "Export", Body: "No active session to export yet. Send a prompt first.", Theme: m.theme})
		return m, nil
	}

	sess, err := m.deps.Sessions.Get(m.ctx, m.sessionID)
	if err != nil {
		m.dialogs.Push(&dialog.Text{DialogID: "error", Title: "Export failed", Body: "Could not load session: " + err.Error(), Theme: m.theme})
		return m, nil
	}
	msgs, err := m.deps.Sessions.Messages(m.ctx, m.sessionID)
	if err != nil {
		m.dialogs.Push(&dialog.Text{DialogID: "error", Title: "Export failed", Body: "Could not load messages: " + err.Error(), Theme: m.theme})
		return m, nil
	}

	rendered, ext, err := renderExport(sess, msgs, format)
	if err != nil {
		m.dialogs.Push(&dialog.Text{DialogID: "error", Title: "Export failed", Body: err.Error(), Theme: m.theme})
		return m, nil
	}

	path := filepath.Join(m.workspaceDir(), fmt.Sprintf("bharatcode-session-%s.%s", shortSessionID(sess.ID), ext))
	if err := os.WriteFile(path, []byte(rendered), 0o644); err != nil {
		m.dialogs.Push(&dialog.Text{DialogID: "error", Title: "Export failed", Body: "Could not write file: " + err.Error(), Theme: m.theme})
		return m, nil
	}

	m.dialogs.Push(&dialog.Text{
		DialogID: "export",
		Title:    "Exported session",
		Body:     "Wrote transcript to:\n" + path,
		Theme:    m.theme,
	})
	return m, nil
}

// renderExport renders the session transcript in the requested format and
// returns the rendered text alongside the file extension to use.
func renderExport(sess *session.Session, msgs []message.Message, format exportFormat) (rendered string, ext string, err error) {
	switch format {
	case exportHTML:
		out, err := session.ExportHTML(sess, msgs)
		if err != nil {
			return "", "", fmt.Errorf("rendering HTML transcript: %w", err)
		}
		return out, "html", nil
	default:
		out, err := session.ExportMarkdown(sess, msgs)
		if err != nil {
			return "", "", fmt.Errorf("rendering Markdown transcript: %w", err)
		}
		return out, "md", nil
	}
}

// parseExportFormat reads the optional format argument from a "/export" or
// "/share" line. An empty argument defaults to Markdown; "md"/"markdown" and
// "html"/"htm" select the respective renderers. Any other argument is an error.
func parseExportFormat(text string) (exportFormat, error) {
	_, args := splitSlash(text)
	switch strings.ToLower(strings.TrimSpace(args)) {
	case "", "md", "markdown":
		return exportMarkdown, nil
	case "html", "htm":
		return exportHTML, nil
	default:
		return exportMarkdown, fmt.Errorf("unknown export format %q (use md or html)", args)
	}
}

// workspaceDir is the directory transcript exports are written into. It is the
// model's configured exportDir when set (tests inject a temp directory),
// otherwise the current working directory (the workspace the TUI was launched
// in). It falls back to "." when the working directory cannot be determined.
func (m *model) workspaceDir() string {
	if m.exportDir != "" {
		return m.exportDir
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}
