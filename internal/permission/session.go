package permission

import "strings"

// PermissionKey identifies a single permission grant scoped to one session. A
// remembered *session* decision is stored under this composite so that approving
// a tool in one session never silently approves it in another: grants no longer
// leak across sessions. Project- and global-scope grants are deliberately NOT
// session-keyed — they are meant to span sessions — so only ScopeSession memory
// uses this key.
//
// Action is a coarse classification of what the tool does (read / write /
// execute / fetch / invoke); Path is the resource the grant applies to (an
// absolute file path for edit/write/view, a command head for bash, a host for
// web tools), empty when the tool is not resource-scoped.
type PermissionKey struct {
	SessionID string
	Tool      string
	Action    string
	Path      string
}

// keyFieldSep separates PermissionKey fields in the canonical string form. It is
// the ASCII unit separator (0x1f), which never appears in a tool name, path, or
// command head, so the joined form is unambiguous.
const keyFieldSep = "\x1f"

// String renders the key as a single stable map key. The field separator cannot
// occur in any field, so distinct keys never collide.
func (k PermissionKey) String() string {
	return strings.Join([]string{k.SessionID, k.Tool, k.Action, k.Path}, keyFieldSep)
}

// actionForTool classifies a tool into a coarse permission action. The grouping
// mirrors the read-class allowlist used elsewhere in this package and is used
// only to populate PermissionKey.Action; resolution correctness rests on Tool
// and Path, so the classification is informative rather than load-bearing.
func actionForTool(tool string) string {
	switch {
	case tool == "bash":
		return "execute"
	case tool == "edit" || tool == "write":
		return "write"
	case tool == "web_fetch" || tool == "web_search":
		return "fetch"
	case readClassTools[tool]:
		return "read"
	default:
		return "invoke"
	}
}

// sessionGrantKey builds the session-scoped storage key for a (sessionID, tool,
// matchKey) triple. matchKey is the canonical "tool:detail" form produced by
// getMatchKey (e.g. "bash:rm", "edit:/abs/path", "web_fetch:host"); the detail
// becomes the PermissionKey Path. Both RememberDecision (store) and the resolve
// helpers (lookup) call this so a grant is found under exactly the key it was
// written with.
func sessionGrantKey(sessionID, tool, matchKey string) string {
	path := ""
	if prefix := tool + ":"; strings.HasPrefix(matchKey, prefix) {
		path = strings.TrimPrefix(matchKey, prefix)
	} else if matchKey != tool {
		// Unprefixed and not the bare tool name: keep it whole rather than lose it.
		path = matchKey
	}
	return PermissionKey{SessionID: sessionID, Tool: tool, Action: actionForTool(tool), Path: path}.String()
}

// SetAutoApproveSession turns per-session auto-approval on or off for sessionID.
// This is the session-scoped form of YOLO: --yolo (and the in-UI yolo toggle)
// flip auto-approval for the active session only, rather than a single global
// switch, so one session can run unattended while another keeps prompting. An
// empty sessionID is ignored.
func (c *Checker) SetAutoApproveSession(sessionID string, on bool) {
	if sessionID == "" {
		return
	}
	if on {
		c.autoApprove.Store(sessionID, struct{}{})
	} else {
		c.autoApprove.Delete(sessionID)
	}
}

// IsAutoApproveSession reports whether sessionID is currently auto-approved. It
// is the read companion to SetAutoApproveSession, letting a UI seam show the
// per-session yolo affordance. An empty sessionID is never auto-approved.
func (c *Checker) IsAutoApproveSession(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	_, ok := c.autoApprove.Load(sessionID)
	return ok
}
