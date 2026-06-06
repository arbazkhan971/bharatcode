package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newMemoryDeps(t *testing.T) Dependencies {
	t.Helper()
	dir := t.TempDir()
	return Dependencies{WorkDir: dir}
}

func runMemory(t *testing.T, deps Dependencies, args map[string]any) Result {
	t.Helper()
	tool := newMemoryTool(deps)
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	res, err := tool.Run(context.Background(), raw)
	if err != nil {
		t.Fatalf("tool.Run: %v", err)
	}
	return res
}

func TestMemoryTool_Name(t *testing.T) {
	deps := newMemoryDeps(t)
	tool := newMemoryTool(deps)
	if tool.Name() != "memory" {
		t.Fatalf("expected name 'memory', got %q", tool.Name())
	}
}

func TestMemoryTool_WriteRead(t *testing.T) {
	deps := newMemoryDeps(t)
	// Override global path so tests don't touch ~/.config
	tool := newMemoryTool(deps).(*memoryTool)
	tool.globalPath = filepath.Join(deps.WorkDir, "global_memory.json")

	rawWrite, _ := json.Marshal(map[string]any{
		"action":  "write",
		"key":     "test_framework",
		"content": "Uses pytest for all Python tests.",
	})
	res, _ := tool.Run(context.Background(), rawWrite)
	if res.IsError {
		t.Fatalf("write failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "test_framework") {
		t.Errorf("expected key in write response, got: %s", res.Content)
	}

	rawRead, _ := json.Marshal(map[string]any{
		"action": "read",
		"key":    "test_framework",
	})
	res, _ = tool.Run(context.Background(), rawRead)
	if res.IsError {
		t.Fatalf("read failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "pytest") {
		t.Errorf("expected content in read response, got: %s", res.Content)
	}
}

func TestMemoryTool_WriteDelete(t *testing.T) {
	deps := newMemoryDeps(t)
	tool := newMemoryTool(deps).(*memoryTool)
	tool.globalPath = filepath.Join(deps.WorkDir, "global_memory.json")

	// Write then delete
	rawWrite, _ := json.Marshal(map[string]any{"action": "write", "key": "to_delete", "content": "temporary"})
	tool.Run(context.Background(), rawWrite) //nolint

	rawDel, _ := json.Marshal(map[string]any{"action": "delete", "key": "to_delete"})
	res, _ := tool.Run(context.Background(), rawDel)
	if res.IsError {
		t.Fatalf("delete failed: %s", res.Content)
	}

	rawRead, _ := json.Marshal(map[string]any{"action": "read", "key": "to_delete"})
	res, _ = tool.Run(context.Background(), rawRead)
	if !res.IsError {
		t.Errorf("expected error reading deleted key, got: %s", res.Content)
	}
}

func TestMemoryTool_List(t *testing.T) {
	deps := newMemoryDeps(t)
	tool := newMemoryTool(deps).(*memoryTool)
	tool.globalPath = filepath.Join(deps.WorkDir, "global_memory.json")

	// Empty list
	res := runMemory(t, deps, map[string]any{"action": "list"})
	// Rerun via concrete tool since runMemory uses default paths
	rawList, _ := json.Marshal(map[string]any{"action": "list"})
	res, _ = tool.Run(context.Background(), rawList)
	if res.IsError || !strings.Contains(res.Content, "No memory") {
		// Either no-entry message or a valid listing
	}

	// Write a couple of entries
	for _, k := range []string{"alpha", "beta"} {
		r, _ := json.Marshal(map[string]any{"action": "write", "key": k, "content": "value " + k})
		tool.Run(context.Background(), r) //nolint
	}

	res, _ = tool.Run(context.Background(), rawList)
	if res.IsError {
		t.Fatalf("list failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "alpha") || !strings.Contains(res.Content, "beta") {
		t.Errorf("expected both keys in list, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "2)") {
		t.Errorf("expected count in list header, got: %s", res.Content)
	}
}

func TestMemoryTool_ProjectScope(t *testing.T) {
	deps := newMemoryDeps(t)
	tool := newMemoryTool(deps).(*memoryTool)
	// project path points inside WorkDir/.bharatcode/memory.json
	expectedPath := filepath.Join(deps.WorkDir, ".bharatcode", "memory.json")
	if tool.projectPath != expectedPath {
		t.Fatalf("expected project path %q, got %q", expectedPath, tool.projectPath)
	}

	rawWrite, _ := json.Marshal(map[string]any{
		"action":  "write",
		"key":     "proj_key",
		"content": "project-scoped note",
		"scope":   "project",
	})
	res, _ := tool.Run(context.Background(), rawWrite)
	if res.IsError {
		t.Fatalf("project write failed: %s", res.Content)
	}

	if _, err := os.Stat(expectedPath); err != nil {
		t.Fatalf("expected project memory file at %s, got error: %v", expectedPath, err)
	}
}

func TestMemoryTool_WriteOverwrite(t *testing.T) {
	deps := newMemoryDeps(t)
	tool := newMemoryTool(deps).(*memoryTool)
	tool.globalPath = filepath.Join(deps.WorkDir, "global_memory.json")

	write := func(content string) {
		r, _ := json.Marshal(map[string]any{"action": "write", "key": "overwrite_me", "content": content})
		res, _ := tool.Run(context.Background(), r)
		if res.IsError {
			t.Fatalf("write failed: %s", res.Content)
		}
	}

	write("first value")
	write("second value")

	rawRead, _ := json.Marshal(map[string]any{"action": "read", "key": "overwrite_me"})
	res, _ := tool.Run(context.Background(), rawRead)
	if !strings.Contains(res.Content, "second value") {
		t.Errorf("expected overwritten value, got: %s", res.Content)
	}
	if strings.Contains(res.Content, "first value") {
		t.Errorf("old value should be gone, got: %s", res.Content)
	}
}

func TestMemoryTool_ErrorCases(t *testing.T) {
	deps := newMemoryDeps(t)
	tool := newMemoryTool(deps).(*memoryTool)
	tool.globalPath = filepath.Join(deps.WorkDir, "global_memory.json")

	cases := []struct {
		name string
		args map[string]any
	}{
		{"write no key", map[string]any{"action": "write", "content": "something"}},
		{"write no content", map[string]any{"action": "write", "key": "k"}},
		{"read no key", map[string]any{"action": "read"}},
		{"delete no key", map[string]any{"action": "delete"}},
		{"unknown action", map[string]any{"action": "upsert"}},
		{"read missing key", map[string]any{"action": "read", "key": "does_not_exist"}},
		{"delete missing key", map[string]any{"action": "delete", "key": "does_not_exist"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := json.Marshal(tc.args)
			res, err := tool.Run(context.Background(), raw)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !res.IsError {
				t.Errorf("expected error result for %q, got: %s", tc.name, res.Content)
			}
		})
	}
}

func TestMemoryTool_Persistence(t *testing.T) {
	deps := newMemoryDeps(t)
	path := filepath.Join(deps.WorkDir, "persist_test.json")

	write := func() {
		tool := newMemoryTool(deps).(*memoryTool)
		tool.globalPath = path
		r, _ := json.Marshal(map[string]any{"action": "write", "key": "persist_key", "content": "survived restart"})
		res, _ := tool.Run(context.Background(), r)
		if res.IsError {
			t.Fatalf("write failed: %s", res.Content)
		}
	}
	read := func() {
		tool := newMemoryTool(deps).(*memoryTool)
		tool.globalPath = path
		r, _ := json.Marshal(map[string]any{"action": "read", "key": "persist_key"})
		res, _ := tool.Run(context.Background(), r)
		if res.IsError || !strings.Contains(res.Content, "survived restart") {
			t.Errorf("persistence failed, got: %s", res.Content)
		}
	}

	write()
	read() // simulates a fresh tool instance on "next session"
}
