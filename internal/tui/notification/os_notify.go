package notification

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// notifyTimeout is the maximum time a notification dispatch may block.
// System notification daemons are typically instant; the cap prevents a
// hung daemon from stalling the TUI.
const notifyTimeout = 3 * time.Second

// SystemNotifier dispatches notifications via the OS-native mechanism:
//   - Linux  → notify-send (libnotify); silently ignored when not installed.
//   - macOS  → osascript display notification.
//   - other  → no-op.
//
// Errors are returned to the caller (FocusAware discards them) so tests can
// inspect them without the process exiting.
type SystemNotifier struct{}

// Notify dispatches a desktop notification. It returns an error if the
// underlying OS command fails or times out, but the caller is not required
// to handle it — the TUI discards all notification errors.
func (SystemNotifier) Notify(title, body string) error {
	ctx, cancel := context.WithTimeout(context.Background(), notifyTimeout)
	defer cancel()
	switch runtime.GOOS {
	case "linux":
		return exec.CommandContext(ctx, "notify-send", "--app-name=bharatcode", title, body).Run()
	case "darwin":
		script := `display notification "` + escapeAppleScript(body) + `" with title "` + escapeAppleScript(title) + `"`
		return exec.CommandContext(ctx, "osascript", "-e", script).Run()
	}
	return nil
}

// escapeAppleScript escapes double-quotes and backslashes so the notification
// body is safe to embed inside an AppleScript string literal.
func escapeAppleScript(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}
