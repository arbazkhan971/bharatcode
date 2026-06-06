package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/stretchr/testify/require"
)

// TestJobListEmpty asserts that with no background jobs the tool reports an
// empty list rather than erroring, with an empty jobs metadata slice.
func TestJobListEmpty(t *testing.T) {
	registry := NewRegistry(shellDeps(t, &config.Config{
		Permissions: config.PermConfig{AllowAll: true},
	}))
	listTool, ok := registry.Get("job_list")
	require.True(t, ok)

	result, err := listTool.Run(context.Background(), json.RawMessage(`{}`))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Contains(t, result.Content, "No background jobs")
	jobs, ok := result.Metadata["jobs"].([]any)
	require.True(t, ok)
	require.Empty(t, jobs)
}

// TestJobListReportsRunningJob asserts a background job started via bash shows
// up in job_list with its id, status, and command — the recovery path when a
// job id has been lost.
func TestJobListReportsRunningJob(t *testing.T) {
	registry := NewRegistry(shellDeps(t, &config.Config{
		Permissions: config.PermConfig{AllowAll: true},
	}))
	bashTool, ok := registry.Get("bash")
	require.True(t, ok)
	listTool, ok := registry.Get("job_list")
	require.True(t, ok)

	start, err := bashTool.Run(context.Background(), json.RawMessage(`{"command":"sleep 10","background":true}`))
	require.NoError(t, err)
	require.False(t, start.IsError)
	jobID, ok := start.Metadata["job_id"].(string)
	require.True(t, ok)

	result, err := listTool.Run(context.Background(), json.RawMessage(`{}`))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Contains(t, result.Content, jobID)
	require.Contains(t, result.Content, "sleep 10")

	jobs, ok := result.Metadata["jobs"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, jobs, 1)
	require.Equal(t, jobID, jobs[0]["job_id"])
}

// TestJobListOrderedNewestFirst asserts multiple jobs are listed newest-started
// first so the most recently launched job is at the top.
func TestJobListOrderedNewestFirst(t *testing.T) {
	registry := NewRegistry(shellDeps(t, &config.Config{
		Permissions: config.PermConfig{AllowAll: true},
	}))
	bashTool, ok := registry.Get("bash")
	require.True(t, ok)
	listTool, ok := registry.Get("job_list")
	require.True(t, ok)

	first, err := bashTool.Run(context.Background(), json.RawMessage(`{"command":"sleep 11","background":true}`))
	require.NoError(t, err)
	firstID := first.Metadata["job_id"].(string)

	// Ensure a distinct start timestamp so ordering is deterministic.
	time.Sleep(5 * time.Millisecond)

	second, err := bashTool.Run(context.Background(), json.RawMessage(`{"command":"sleep 12","background":true}`))
	require.NoError(t, err)
	secondID := second.Metadata["job_id"].(string)

	result, err := listTool.Run(context.Background(), json.RawMessage(`{}`))
	require.NoError(t, err)
	require.False(t, result.IsError)

	idxFirst := strings.Index(result.Content, firstID)
	idxSecond := strings.Index(result.Content, secondID)
	require.NotEqual(t, -1, idxFirst)
	require.NotEqual(t, -1, idxSecond)
	require.Less(t, idxSecond, idxFirst, "newest-started job should be listed first")
}
