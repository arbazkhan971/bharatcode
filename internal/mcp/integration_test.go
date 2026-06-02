//go:build integration

package mcp

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/permission"
	"github.com/stretchr/testify/require"
)

func TestFilesystemServerIntegration(t *testing.T) {
	if _, err := exec.LookPath("npx"); err != nil {
		t.Skip("npx is not available")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "fixture.txt")
	require.NoError(t, os.WriteFile(path, []byte("fixture-data"), 0o644))

	cfg := &config.Config{
		MCP: []config.MCPServer{{
			Name:      "filesystem",
			Transport: "stdio",
			Command:   "npx",
			Args:      []string{"-y", "@modelcontextprotocol/server-filesystem", dir},
		}},
		Permissions: config.PermConfig{AllowAll: true},
	}
	client := NewClient(cfg, permission.New(cfg, nil), nil)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, client.Start(ctx))
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		_ = client.Stop(stopCtx)
	})

	found := false
	for _, tool := range client.Tools() {
		if tool.Name() == "filesystem__read_file" {
			found = true
			result, err := tool.Run(ctx, json.RawMessage(`{"path":"`+path+`"}`))
			require.NoError(t, err)
			require.False(t, result.IsError)
			require.Contains(t, result.Content, "fixture-data")
		}
	}
	require.True(t, found, "filesystem__read_file not found")
}
