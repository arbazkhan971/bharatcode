package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/tools"
)

// taskToolName is the canonical name of the subagent-dispatch tool.
const taskToolName = "task"

// defaultAgent is the subagent used when a task does not name one. It is the
// read-only investigation agent, the safest default: a dispatched subtask
// without an explicit type cannot mutate the workspace.
const defaultAgent = "task"

// defaultTaskConcurrency bounds how many dispatched subagents run at once when
// a single task call fans out to several subtasks. It keeps a wide fan-out from
// hammering the provider with unbounded concurrent requests while still letting
// independent investigations overlap.
const defaultTaskConcurrency = 4

// dispatchFunc runs a batch of subtasks and returns one Result per task in
// input order. It mirrors Coordinator.DispatchParallel so the tool can be unit
// tested with a stub instead of a fully wired Coordinator.
type dispatchFunc func(ctx context.Context, tasks []SubTask, limit int) ([]Result, error)

// taskTool lets an agent delegate self-contained units of work to subagents,
// optionally several at once running in parallel. Each subtask runs in a fresh
// session with no prior context, so the calling agent can fan out independent
// investigations (or scoped edits, via a writing subagent) and collect their
// conclusions without bloating its own context window. It is the LLM-callable
// surface over Coordinator.DispatchParallel.
type taskTool struct {
	dispatch    dispatchFunc
	agents      []string
	concurrency int
}

// newTaskTool constructs a taskTool over the given dispatch function and the
// set of agent names that may be named as a subtask's subagent_type. The agent
// list is sorted and deduplicated for a stable description and validation set;
// dispatch must be non-nil.
func newTaskTool(dispatch dispatchFunc, agents []string) *taskTool {
	seen := make(map[string]struct{}, len(agents))
	uniq := make([]string, 0, len(agents))
	for _, name := range agents {
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		uniq = append(uniq, name)
	}
	sort.Strings(uniq)
	return &taskTool{dispatch: dispatch, agents: uniq, concurrency: defaultTaskConcurrency}
}

// Name returns the canonical tool name used in tool-call requests.
func (t *taskTool) Name() string {
	return taskToolName
}

// Description returns the markdown description shown to the model, including
// the set of agent types a subtask may target.
func (t *taskTool) Description() string {
	var b strings.Builder
	b.WriteString("Delegate self-contained work to one or more subagents. ")
	b.WriteString("Provide a `tasks` array; each task runs in its own fresh session ")
	b.WriteString("with no access to this conversation, so every prompt must be complete ")
	b.WriteString("and self-contained. Multiple tasks run in parallel, which is ideal for ")
	b.WriteString("independent investigations (\"find every caller of X\", \"summarize package Y\"). ")
	b.WriteString("Each subtask returns only its final answer, keeping bulky intermediate ")
	b.WriteString("work out of your context. Prefer one task call with several tasks over ")
	b.WriteString("many separate calls when the work is independent.\n\n")
	if len(t.agents) > 0 {
		b.WriteString("Available subagent_type values: ")
		b.WriteString(strings.Join(t.agents, ", "))
		b.WriteString(". Defaults to \"")
		b.WriteString(defaultAgent)
		b.WriteString("\", a read-only investigator that cannot modify files.")
	}
	return b.String()
}

// taskToolSchema is the JSON Schema for the task tool's arguments.
var taskToolSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "tasks": {
      "type": "array",
      "minItems": 1,
      "description": "Independent units of work to dispatch. More than one runs in parallel.",
      "items": {
        "type": "object",
        "properties": {
          "description": {
            "type": "string",
            "description": "Short 3-6 word title for the task, used to label its result."
          },
          "prompt": {
            "type": "string",
            "description": "The full, self-contained instruction for the subagent. It runs with no prior context, so include every detail it needs."
          },
          "subagent_type": {
            "type": "string",
            "description": "Which agent to run the task as. Defaults to the read-only \"task\" investigator."
          }
        },
        "required": ["prompt"]
      }
    }
  },
  "required": ["tasks"]
}`)

// Schema returns the JSON Schema for the tool's arguments.
func (t *taskTool) Schema() json.RawMessage {
	return append(json.RawMessage(nil), taskToolSchema...)
}

// taskSpec is one decoded entry from the task tool's arguments.
type taskSpec struct {
	Description  string `json:"description"`
	Prompt       string `json:"prompt"`
	SubagentType string `json:"subagent_type"`
}

// taskArgs is the decoded argument shape for the task tool.
type taskArgs struct {
	Tasks []taskSpec `json:"tasks"`
}

// Run validates the requested subtasks, dispatches them via the injected
// dispatch function, and renders one labeled section per result. Validation
// failures (no tasks, an empty prompt, an unknown subagent_type) return an
// error result rather than a Go error so the agent loop can surface them to the
// model and continue. A per-subtask failure is reported inline in its section;
// the aggregate result is only marked an error when every subtask failed.
func (t *taskTool) Run(ctx context.Context, args json.RawMessage) (tools.Result, error) {
	var decoded taskArgs
	if len(strings.TrimSpace(string(args))) > 0 {
		if err := json.Unmarshal(args, &decoded); err != nil {
			return tools.Result{Content: "invalid task arguments: " + err.Error(), IsError: true}, nil
		}
	}
	if len(decoded.Tasks) == 0 {
		return tools.Result{Content: "task requires at least one entry in \"tasks\"", IsError: true}, nil
	}

	subtasks := make([]SubTask, 0, len(decoded.Tasks))
	for i, spec := range decoded.Tasks {
		prompt := strings.TrimSpace(spec.Prompt)
		if prompt == "" {
			return tools.Result{Content: fmt.Sprintf("task %d: prompt is required", i+1), IsError: true}, nil
		}
		agent := strings.TrimSpace(spec.SubagentType)
		if agent == "" {
			agent = defaultAgent
		}
		if !t.knownAgent(agent) {
			return tools.Result{
				Content: fmt.Sprintf("task %d: unknown subagent_type %q; available: %s",
					i+1, agent, strings.Join(t.agents, ", ")),
				IsError: true,
			}, nil
		}
		subtasks = append(subtasks, SubTask{
			Agent:  agent,
			Prompt: prompt,
			Title:  strings.TrimSpace(spec.Description),
		})
	}

	limit := t.concurrency
	if limit <= 0 || limit > len(subtasks) {
		limit = len(subtasks)
	}
	results, _ := t.dispatch(ctx, subtasks, limit)

	return renderTaskResults(subtasks, results), nil
}

// knownAgent reports whether name is a dispatchable agent. When no agent set is
// configured it accepts any name, deferring validation to dispatch.
func (t *taskTool) knownAgent(name string) bool {
	if len(t.agents) == 0 {
		return true
	}
	for _, a := range t.agents {
		if a == name {
			return true
		}
	}
	return false
}

// renderTaskResults formats the dispatched results into a single tool result,
// one section per subtask labeled by its title (falling back to the agent name)
// and reporting per-subtask errors inline. The aggregate is marked an error
// only when every subtask failed, so partial success still returns the
// successful outputs to the model.
func renderTaskResults(subtasks []SubTask, results []Result) tools.Result {
	var b strings.Builder
	noun := "subagent task"
	if len(subtasks) != 1 {
		noun = "subagent tasks"
	}
	fmt.Fprintf(&b, "Ran %d %s.\n", len(subtasks), noun)

	failures := 0
	for i, st := range subtasks {
		title := st.Title
		if title == "" {
			title = st.Agent
		}
		fmt.Fprintf(&b, "\n## %d. %s\n", i+1, title)

		var res Result
		if i < len(results) {
			res = results[i]
		}
		switch {
		case res.Err != nil:
			failures++
			fmt.Fprintf(&b, "error: %s\n", res.Err.Error())
		case strings.TrimSpace(res.Output) == "":
			fmt.Fprint(&b, "(no output)\n")
		default:
			b.WriteString(strings.TrimSpace(res.Output))
			b.WriteByte('\n')
		}
	}

	return tools.Result{
		Content: strings.TrimRight(b.String(), "\n"),
		IsError: failures == len(subtasks),
	}
}
