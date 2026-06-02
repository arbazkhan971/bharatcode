package tui

import (
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/permission"
	"github.com/arbazkhan971/bharatcode/internal/tui/dialog"
	tea "github.com/charmbracelet/bubbletea/v2"
)

// handlePermissionsCommand drives the /permissions slash command: it shows the
// current approval mode, sets a named mode live, or cycles to the next mode,
// reflecting the change in the status bar.
func (m *model) handlePermissionsCommand(text string) tea.Model {
	args := strings.TrimSpace(strings.TrimPrefix(text, "/permissions"))

	if args == "" {
		m.dialogs.Push(&dialog.Text{
			DialogID: "permissions",
			Title:    "Permissions",
			Body:     "Approval mode: " + approvalModeLabel(m.deps.Permission.GetApprovalMode()) + "\nUse /permissions read-only|auto|full to change.",
			Theme:    m.theme,
		})
		return m
	}

	mode, ok := parseApprovalMode(args)
	if !ok {
		if strings.EqualFold(args, "cycle") {
			mode = nextApprovalMode(m.deps.Permission.GetApprovalMode())
		} else {
			m.dialogs.Push(&dialog.Text{
				DialogID: "permissions",
				Title:    "Permissions",
				Body:     "Unknown mode " + args + ". Use read-only, auto, or full.",
				Theme:    m.theme,
			})
			return m
		}
	}

	m.deps.Permission.SetApprovalMode(mode)
	m.applyApprovalMode(mode)
	m.dialogs.Push(&dialog.Text{
		DialogID: "permissions",
		Title:    "Permissions",
		Body:     "Approval mode set to " + approvalModeLabel(mode) + ".",
		Theme:    m.theme,
	})
	return m
}

// applyApprovalMode reflects the active approval mode in the status bar and
// keeps the legacy yolo flag in sync (Full mode is the yolo equivalent).
func (m *model) applyApprovalMode(mode permission.ApprovalMode) {
	m.status.Mode = approvalModeLabel(mode)
	m.status.Yolo = mode == permission.ApprovalFull
}

// parseApprovalMode maps a user token to an ApprovalMode.
func parseApprovalMode(s string) (permission.ApprovalMode, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "read-only", "readonly", "read", "ro":
		return permission.ApprovalReadOnly, true
	case "auto", "ask", "default":
		return permission.ApprovalAuto, true
	case "full", "yolo", "all":
		return permission.ApprovalFull, true
	default:
		return "", false
	}
}

// nextApprovalMode cycles read-only -> auto -> full -> read-only.
func nextApprovalMode(mode permission.ApprovalMode) permission.ApprovalMode {
	switch mode {
	case permission.ApprovalReadOnly:
		return permission.ApprovalAuto
	case permission.ApprovalAuto:
		return permission.ApprovalFull
	case permission.ApprovalFull:
		return permission.ApprovalReadOnly
	default:
		return permission.ApprovalAuto
	}
}

// approvalModeLabel renders an ApprovalMode for the status bar and dialogs.
func approvalModeLabel(mode permission.ApprovalMode) string {
	switch mode {
	case permission.ApprovalReadOnly:
		return "read-only"
	case permission.ApprovalAuto:
		return "auto"
	case permission.ApprovalFull:
		return "full"
	default:
		return "auto"
	}
}
