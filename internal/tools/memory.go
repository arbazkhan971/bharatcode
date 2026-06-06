package tools

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// memoryEntry is one persisted note stored in the memory file.
type memoryEntry struct {
	Content   string `json:"content"`
	UpdatedAt string `json:"updated_at"`
}

// memoryFile is the on-disk representation of the memory store.
type memoryFile struct {
	Entries map[string]memoryEntry `json:"entries"`
}

type memoryTool struct {
	globalPath  string
	projectPath string
}

type memoryArgs struct {
	Action  string `json:"action"`
	Key     string `json:"key,omitempty"`
	Content string `json:"content,omitempty"`
	Scope   string `json:"scope,omitempty"`
}

var (
	memoryFileMu sync.Mutex

	schemaMemory = json.RawMessage(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["action"],
  "properties": {
    "action": {
      "type": "string",
      "enum": ["write", "read", "delete", "list"],
      "description": "write: store a note; read: retrieve a note by key; delete: remove a note; list: show all keys with previews."
    },
    "key": {
      "type": "string",
      "description": "Unique name for the note (required for write, read, delete). Use short, descriptive snake_case names like 'user_prefers_tabs' or 'test_framework'."
    },
    "content": {
      "type": "string",
      "description": "Note content (required for write). Plain text, markdown OK. No length limit."
    },
    "scope": {
      "type": "string",
      "enum": ["global", "project"],
      "description": "global (default): note survives across all projects; project: note is scoped to this working directory's .bharatcode/ folder."
    }
  }
}`)
)

//go:embed memory.md
var memoryDescription string

func newMemoryTool(deps Dependencies) Tool {
	globalPath := globalMemoryPath()
	projectPath := ""
	if deps.WorkDir != "" {
		projectPath = filepath.Join(deps.WorkDir, ".bharatcode", "memory.json")
	}
	return &memoryTool{globalPath: globalPath, projectPath: projectPath}
}

// globalMemoryPath returns the path to the global memory file, adjacent to the
// global config file (~/.config/bharatcode/memory.json or equivalent).
func globalMemoryPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	xdg := os.Getenv("XDG_CONFIG_HOME")
	if xdg != "" {
		return filepath.Join(xdg, "bharatcode", "memory.json")
	}
	return filepath.Join(home, ".config", "bharatcode", "memory.json")
}

func (t *memoryTool) Name() string { return "memory" }

func (t *memoryTool) Description() string { return memoryDescription }

func (t *memoryTool) Schema() json.RawMessage { return schemaMemory }

func (t *memoryTool) Run(ctx context.Context, raw json.RawMessage) (res Result, err error) {
	defer recoverTool(ctx, t.Name(), &res, &err)

	args, errResult := decodeArgs[memoryArgs](raw)
	if errResult != nil {
		return *errResult, nil
	}
	args.Action = strings.ToLower(strings.TrimSpace(args.Action))
	args.Key = strings.TrimSpace(args.Key)
	args.Scope = strings.ToLower(strings.TrimSpace(args.Scope))
	if args.Scope == "" {
		args.Scope = "global"
	}

	path := t.globalPath
	if args.Scope == "project" {
		if t.projectPath == "" {
			return errorResult("project scope requires a working directory"), nil
		}
		path = t.projectPath
	}
	if path == "" {
		return errorResult("cannot resolve memory file path"), nil
	}

	switch args.Action {
	case "write":
		return t.write(path, args)
	case "read":
		return t.read(path, args)
	case "delete":
		return t.deleteEntry(path, args)
	case "list":
		return t.list(path)
	default:
		return errorResult("action must be write, read, delete, or list"), nil
	}
}

func (t *memoryTool) write(path string, args memoryArgs) (Result, error) {
	if args.Key == "" {
		return errorResult("key is required for write"), nil
	}
	if args.Content == "" {
		return errorResult("content is required for write"), nil
	}

	memoryFileMu.Lock()
	defer memoryFileMu.Unlock()

	mf, err := loadMemoryFile(path)
	if err != nil {
		return errorResult("failed to load memory file: " + err.Error()), nil
	}
	mf.Entries[args.Key] = memoryEntry{
		Content:   args.Content,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := saveMemoryFile(path, mf); err != nil {
		return errorResult("failed to save memory: " + err.Error()), nil
	}
	return Result{Content: fmt.Sprintf("Memory saved: %q (%s scope)", args.Key, args.Scope)}, nil
}

func (t *memoryTool) read(path string, args memoryArgs) (Result, error) {
	if args.Key == "" {
		return errorResult("key is required for read (use list to see all keys)"), nil
	}

	memoryFileMu.Lock()
	defer memoryFileMu.Unlock()

	mf, err := loadMemoryFile(path)
	if err != nil {
		return errorResult("failed to load memory file: " + err.Error()), nil
	}
	entry, ok := mf.Entries[args.Key]
	if !ok {
		return errorResult(fmt.Sprintf("no memory entry found for key %q (scope: %s)", args.Key, args.Scope)), nil
	}
	return Result{Content: fmt.Sprintf("Memory %q (updated %s):\n%s", args.Key, entry.UpdatedAt, entry.Content)}, nil
}

func (t *memoryTool) deleteEntry(path string, args memoryArgs) (Result, error) {
	if args.Key == "" {
		return errorResult("key is required for delete"), nil
	}

	memoryFileMu.Lock()
	defer memoryFileMu.Unlock()

	mf, err := loadMemoryFile(path)
	if err != nil {
		return errorResult("failed to load memory file: " + err.Error()), nil
	}
	if _, ok := mf.Entries[args.Key]; !ok {
		return errorResult(fmt.Sprintf("no memory entry found for key %q", args.Key)), nil
	}
	delete(mf.Entries, args.Key)
	if err := saveMemoryFile(path, mf); err != nil {
		return errorResult("failed to save memory: " + err.Error()), nil
	}
	return Result{Content: fmt.Sprintf("Memory deleted: %q", args.Key)}, nil
}

func (t *memoryTool) list(path string) (Result, error) {
	memoryFileMu.Lock()
	defer memoryFileMu.Unlock()

	mf, err := loadMemoryFile(path)
	if err != nil {
		return errorResult("failed to load memory file: " + err.Error()), nil
	}
	if len(mf.Entries) == 0 {
		return Result{Content: "No memory entries."}, nil
	}
	keys := make([]string, 0, len(mf.Entries))
	for k := range mf.Entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sb strings.Builder
	fmt.Fprintf(&sb, "Memory entries (%d):\n", len(keys))
	for _, k := range keys {
		entry := mf.Entries[k]
		preview := entry.Content
		if len(preview) > 80 {
			preview = preview[:77] + "..."
		}
		preview = strings.ReplaceAll(preview, "\n", " ")
		fmt.Fprintf(&sb, "- %s (updated %s): %s\n", k, entry.UpdatedAt, preview)
	}
	return Result{Content: strings.TrimRight(sb.String(), "\n")}, nil
}

func loadMemoryFile(path string) (*memoryFile, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &memoryFile{Entries: make(map[string]memoryEntry)}, nil
	}
	if err != nil {
		return nil, err
	}
	var mf memoryFile
	if err := json.Unmarshal(data, &mf); err != nil {
		return nil, fmt.Errorf("memory file corrupt: %w", err)
	}
	if mf.Entries == nil {
		mf.Entries = make(map[string]memoryEntry)
	}
	return &mf, nil
}

func saveMemoryFile(path string, mf *memoryFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(mf, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
