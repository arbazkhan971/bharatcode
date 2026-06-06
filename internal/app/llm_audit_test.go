package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/arbazkhan971/bharatcode/internal/agent"
	"github.com/arbazkhan971/bharatcode/internal/audit"
)

// TestLLMAuditLoggerAppendsImmutableRecord pins the app-side adapter to the
// audit store: a reported provider turn lands as a verifiable TypeLLM entry
// carrying the destination provider/model, sizes, and error flag.
func TestLLMAuditLoggerAppendsImmutableRecord(t *testing.T) {
	ctx := context.Background()
	store, err := audit.Open(ctx, filepath.Join(t.TempDir(), "audit.db"))
	require.NoError(t, err)
	defer store.Close()

	logger := llmAuditLogger{store: store}
	logger.LogLLM(ctx, agent.LLMAuditRecord{
		SessionID:    "sess-1",
		Agent:        "coder",
		Provider:     "anthropic",
		Model:        "claude-opus-4-8",
		Messages:     4,
		InputTokens:  1200,
		OutputTokens: 300,
		IsError:      false,
	})
	logger.LogLLM(ctx, agent.LLMAuditRecord{
		SessionID: "sess-1",
		Agent:     "coder",
		Provider:  "ollama",
		Model:     "llama3",
		Messages:  2,
		IsError:   true,
	})

	records, err := store.Records(ctx)
	require.NoError(t, err)
	require.Len(t, records, 2)

	require.Equal(t, audit.TypeLLM, records[0].Type)
	require.Equal(t, "sess-1", records[0].Actor)
	require.Equal(t, "sent prompt to anthropic/claude-opus-4-8", records[0].Summary)
	require.JSONEq(t,
		`{"provider":"anthropic","model":"claude-opus-4-8","messages":4,"input_tokens":1200,"output_tokens":300,"error":false,"agent":"coder"}`,
		string(records[0].Detail))

	require.Equal(t, audit.TypeLLM, records[1].Type)
	require.Equal(t, "failed sending prompt to ollama/llama3", records[1].Summary)
	require.JSONEq(t,
		`{"provider":"ollama","model":"llama3","messages":2,"input_tokens":0,"output_tokens":0,"error":true,"agent":"coder"}`,
		string(records[1].Detail))

	// The records form an intact, verifiable hash chain.
	n, err := store.Verify(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, n)
}

// TestLLMAuditLoggerUnknownProvider confirms an empty provider name is rendered
// as "unknown" rather than leaving the egress destination blank.
func TestLLMAuditLoggerUnknownProvider(t *testing.T) {
	ctx := context.Background()
	store, err := audit.Open(ctx, filepath.Join(t.TempDir(), "audit.db"))
	require.NoError(t, err)
	defer store.Close()

	logger := llmAuditLogger{store: store}
	logger.LogLLM(ctx, agent.LLMAuditRecord{SessionID: "s", Model: "m"})

	records, err := store.Records(ctx)
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.Equal(t, "sent prompt to unknown/m", records[0].Summary)
	require.JSONEq(t,
		`{"provider":"unknown","model":"m","messages":0,"input_tokens":0,"output_tokens":0,"error":false}`,
		string(records[0].Detail))
}

// TestLLMAuditLoggerNilStoreIsSafe confirms the adapter drops records rather
// than panicking when no store is configured.
func TestLLMAuditLoggerNilStoreIsSafe(t *testing.T) {
	logger := llmAuditLogger{store: nil}
	require.NotPanics(t, func() {
		logger.LogLLM(context.Background(), agent.LLMAuditRecord{Provider: "anthropic", Model: "m"})
	})
}
