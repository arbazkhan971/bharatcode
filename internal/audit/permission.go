package audit

import (
	"context"
	"fmt"

	"github.com/arbazkhan971/bharatcode/internal/permission"
)

// PermissionLogger returns a permission.AuditLogger that records every
// permission decision as an immutable audit entry. The returned logger is safe
// for concurrent use; on a write failure it silently drops the record so a
// failing audit log never blocks a permission decision.
func (s *Store) PermissionLogger() permission.AuditLogger {
	return permissionLogger{store: s}
}

type permissionLogger struct {
	store *Store
}

func (l permissionLogger) Log(ctx context.Context, rec permission.AuditRecord) {
	if l.store == nil {
		return
	}
	_, _ = l.store.Append(ctx, Event{
		Type:    TypePermission,
		Actor:   rec.SessionID,
		Summary: fmt.Sprintf("%s %s (scope=%s)", rec.Decision, rec.Tool, rec.Scope),
		Detail: map[string]any{
			"tool":     rec.Tool,
			"decision": string(rec.Decision),
			"scope":    string(rec.Scope),
			"args":     rec.ArgsSummary,
		},
	})
}
