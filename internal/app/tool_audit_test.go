package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/arbazkhan971/bharatcode/internal/agent"
	"github.com/arbazkhan971/bharatcode/internal/audit"
)

// TestToolAuditLoggerAppendsImmutableRecord pins the app-side adapter to the
// audit store: a reported tool invocation lands as a verifiable TypeTool entry
// carrying the tool name, actor, and error flag.
func TestToolAuditLoggerAppendsImmutableRecord(t *testing.T) {
	ctx := context.Background()
	store, err := audit.Open(ctx, filepath.Join(t.TempDir(), "audit.db"))
	require.NoError(t, err)
	defer store.Close()

	logger := toolAuditLogger{store: store}
	logger.LogTool(ctx, agent.ToolAuditRecord{
		SessionID: "sess-1",
		Agent:     "coder",
		Tool:      "bash",
		IsError:   false,
	})
	logger.LogTool(ctx, agent.ToolAuditRecord{
		SessionID: "sess-1",
		Agent:     "coder",
		Tool:      "edit",
		IsError:   true,
	})

	records, err := store.Records(ctx)
	require.NoError(t, err)
	require.Len(t, records, 2)

	require.Equal(t, audit.TypeTool, records[0].Type)
	require.Equal(t, "sess-1", records[0].Actor)
	require.Equal(t, "ran bash", records[0].Summary)
	require.JSONEq(t, `{"tool":"bash","error":false,"agent":"coder"}`, string(records[0].Detail))

	require.Equal(t, audit.TypeTool, records[1].Type)
	require.Equal(t, "failed edit", records[1].Summary)
	require.JSONEq(t, `{"tool":"edit","error":true,"agent":"coder"}`, string(records[1].Detail))

	// The records form an intact, verifiable hash chain.
	n, err := store.Verify(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, n)
}

// TestToolAuditLoggerNilStoreIsSafe confirms the adapter drops records rather
// than panicking when no store is configured.
func TestToolAuditLoggerNilStoreIsSafe(t *testing.T) {
	logger := toolAuditLogger{store: nil}
	require.NotPanics(t, func() {
		logger.LogTool(context.Background(), agent.ToolAuditRecord{Tool: "bash"})
	})
}
