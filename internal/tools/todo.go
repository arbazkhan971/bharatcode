package tools

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/arbazkhan971/bharatcode/internal/pubsub"
)

// TodoEvent reports an in-session todo state change.
type TodoEvent struct {
	Action string     `json:"action"`
	Items  []TodoItem `json:"items"`
}

// TodoItem is one item in the in-session task list.
type TodoItem struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	Status  string `json:"status"`
}

type todoTool struct {
	state *todoState
	bus   *pubsub.Topic[pubsub.ToolCallPayload]
}

type todoState struct {
	mu     sync.Mutex
	nextID int
	items  map[string]TodoItem
}

type todoArgs struct {
	Action string          `json:"action"`
	Items  []todoItemArgs  `json:"items,omitempty"`
	Item   *todoItemArgs   `json:"item,omitempty"`
	ID     string          `json:"id,omitempty"`
	Status string          `json:"status,omitempty"`
	Text   string          `json:"text,omitempty"`
	Raw    json.RawMessage `json:"-"`
}

type todoItemArgs struct {
	ID      string `json:"id,omitempty"`
	Content string `json:"content,omitempty"`
	Text    string `json:"text,omitempty"`
	Status  string `json:"status,omitempty"`
}

var (
	todoStatesMu sync.Mutex
	todoStates   = make(map[*pubsub.Topic[pubsub.ToolCallPayload]]*todoState)
	schemaTodo   = json.RawMessage(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["action"],
  "properties": {
    "action": {"type": "string", "enum": ["add", "update", "delete", "list"]},
    "items": {"type": "array", "items": {"type": "object", "properties": {"id": {"type": "string"}, "content": {"type": "string"}, "text": {"type": "string"}, "status": {"type": "string"}}}},
    "item": {"type": "object"},
    "id": {"type": "string"},
    "status": {"type": "string"},
    "text": {"type": "string"}
  }
}`)
)

//go:embed todo.md
var todoDescription string

func newTodoTool(deps Dependencies) Tool {
	return &todoTool{
		state: todoStateForBus(deps.Bus),
		bus:   deps.Bus,
	}
}

func (t *todoTool) Name() string {
	return "todo"
}

func (t *todoTool) Description() string {
	return todoDescription
}

func (t *todoTool) Schema() json.RawMessage {
	return schemaTodo
}

func (t *todoTool) Run(ctx context.Context, raw json.RawMessage) (res Result, err error) {
	defer recoverTool(ctx, t.Name(), &res, &err)

	var args todoArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return errorResult("invalid todo arguments: " + err.Error()), nil
	}
	args.Action = strings.ToLower(strings.TrimSpace(args.Action))
	if args.Action == "" {
		return errorResult("action is required"), nil
	}

	var content string
	switch args.Action {
	case "add":
		added, err := t.add(args)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		content = formatTodos("Added todo items:", added)
	case "update":
		updated, err := t.update(args)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		content = formatTodos("Updated todo items:", updated)
	case "delete":
		deleted, err := t.delete(args)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		content = formatTodos("Deleted todo items:", deleted)
	case "list":
		items := t.list()
		if len(items) == 0 {
			content = "No todo items."
		} else {
			content = formatTodos("Todo items:", items)
		}
	default:
		return errorResult("action must be add, update, delete, or list"), nil
	}

	t.publish(ctx, args.Action)
	return Result{Content: content}, nil
}

func todoStateForBus(bus *pubsub.Topic[pubsub.ToolCallPayload]) *todoState {
	if bus == nil {
		return &todoState{nextID: 1, items: make(map[string]TodoItem)}
	}
	todoStatesMu.Lock()
	defer todoStatesMu.Unlock()
	if state, ok := todoStates[bus]; ok {
		return state
	}
	state := &todoState{nextID: 1, items: make(map[string]TodoItem)}
	todoStates[bus] = state
	return state
}

func (t *todoTool) add(args todoArgs) ([]TodoItem, error) {
	inputs := args.todoInputs()
	if len(inputs) == 0 && strings.TrimSpace(args.Text) != "" {
		inputs = []todoItemArgs{{Content: args.Text}}
	}
	if len(inputs) == 0 {
		return nil, fmt.Errorf("items are required for add")
	}

	t.state.mu.Lock()
	defer t.state.mu.Unlock()
	var added []TodoItem
	for _, input := range inputs {
		text := strings.TrimSpace(firstNonEmpty(input.Content, input.Text))
		if text == "" {
			return nil, fmt.Errorf("todo content is required")
		}
		status := normalizeTodoStatus(input.Status)
		id := input.ID
		if id == "" {
			id = fmt.Sprintf("%d", t.state.nextID)
			t.state.nextID++
		}
		item := TodoItem{ID: id, Content: text, Status: status}
		t.state.items[id] = item
		added = append(added, item)
	}
	return added, nil
}

func (t *todoTool) update(args todoArgs) ([]TodoItem, error) {
	inputs := args.todoInputs()
	if len(inputs) == 0 && args.ID != "" {
		inputs = []todoItemArgs{{ID: args.ID, Status: args.Status, Content: args.Text}}
	}
	if len(inputs) == 0 {
		return nil, fmt.Errorf("items or id are required for update")
	}

	t.state.mu.Lock()
	defer t.state.mu.Unlock()
	var updated []TodoItem
	for _, input := range inputs {
		if input.ID == "" {
			return nil, fmt.Errorf("todo id is required for update")
		}
		item, ok := t.state.items[input.ID]
		if !ok {
			return nil, fmt.Errorf("todo item %s not found", input.ID)
		}
		if text := strings.TrimSpace(firstNonEmpty(input.Content, input.Text)); text != "" {
			item.Content = text
		}
		if input.Status != "" {
			item.Status = normalizeTodoStatus(input.Status)
		}
		t.state.items[item.ID] = item
		updated = append(updated, item)
	}
	return updated, nil
}

func (t *todoTool) delete(args todoArgs) ([]TodoItem, error) {
	inputs := args.todoInputs()
	if len(inputs) == 0 && args.ID != "" {
		inputs = []todoItemArgs{{ID: args.ID}}
	}
	if len(inputs) == 0 {
		return nil, fmt.Errorf("items or id are required for delete")
	}

	t.state.mu.Lock()
	defer t.state.mu.Unlock()
	var deleted []TodoItem
	for _, input := range inputs {
		if input.ID == "" {
			return nil, fmt.Errorf("todo id is required for delete")
		}
		item, ok := t.state.items[input.ID]
		if !ok {
			return nil, fmt.Errorf("todo item %s not found", input.ID)
		}
		delete(t.state.items, input.ID)
		deleted = append(deleted, item)
	}
	return deleted, nil
}

func (t *todoTool) list() []TodoItem {
	t.state.mu.Lock()
	defer t.state.mu.Unlock()
	items := make([]TodoItem, 0, len(t.state.items))
	for _, item := range t.state.items {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items
}

func (t *todoTool) publish(ctx context.Context, action string) {
	if t.bus == nil {
		return
	}
	t.bus.Publish(ctx, pubsub.ToolCallPayload{})
}

func (a todoArgs) todoInputs() []todoItemArgs {
	if len(a.Items) > 0 {
		return a.Items
	}
	if a.Item != nil {
		return []todoItemArgs{*a.Item}
	}
	return nil
}

func normalizeTodoStatus(status string) string {
	status = strings.ToLower(strings.TrimSpace(status))
	switch status {
	case "", "pending", "todo", "open":
		return "pending"
	case "in_progress", "in-progress", "doing":
		return "in_progress"
	case "done", "completed", "complete":
		return "done"
	default:
		return status
	}
}

func formatTodos(header string, items []TodoItem) string {
	var b strings.Builder
	b.WriteString(header)
	for _, item := range items {
		fmt.Fprintf(&b, "\n- [%s] %s: %s", item.Status, item.ID, item.Content)
	}
	return b.String()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
