package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// recordingDispatch captures the tasks and limit passed to it and returns a
// canned set of results, so a taskTool can be exercised without a wired
// Coordinator.
type recordingDispatch struct {
	gotTasks []SubTask
	gotLimit int
	results  []Result
	err      error
}

func (d *recordingDispatch) run(ctx context.Context, tasks []SubTask, limit int) ([]Result, error) {
	d.gotTasks = tasks
	d.gotLimit = limit
	return d.results, d.err
}

func TestTaskToolDispatchesEachTaskInOrder(t *testing.T) {
	d := &recordingDispatch{results: []Result{
		{Index: 0, Output: "callers of Foo: a.go, b.go"},
		{Index: 1, Output: "package y summary"},
	}}
	tool := newTaskTool(d.run, []string{"coder", "task"})

	args := json.RawMessage(`{"tasks":[
		{"description":"find callers","prompt":"Find every caller of Foo."},
		{"description":"summarize y","prompt":"Summarize package y.","subagent_type":"task"}
	]}`)
	res, err := tool.Run(context.Background(), args)
	require.NoError(t, err)
	require.False(t, res.IsError)

	require.Len(t, d.gotTasks, 2)
	require.Equal(t, "Find every caller of Foo.", d.gotTasks[0].Prompt)
	require.Equal(t, "task", d.gotTasks[0].Agent, "missing subagent_type defaults to the read-only task agent")
	require.Equal(t, "find callers", d.gotTasks[0].Title)
	require.Equal(t, "task", d.gotTasks[1].Agent)

	// Both outputs are present and attributed to their task labels.
	require.Contains(t, res.Content, "## 1. find callers")
	require.Contains(t, res.Content, "callers of Foo: a.go, b.go")
	require.Contains(t, res.Content, "## 2. summarize y")
	require.Contains(t, res.Content, "package y summary")
}

func TestTaskToolBoundsConcurrencyToDefault(t *testing.T) {
	d := &recordingDispatch{}
	// More tasks than the default concurrency cap.
	specs := make([]string, 0, defaultTaskConcurrency+2)
	for i := 0; i < defaultTaskConcurrency+2; i++ {
		specs = append(specs, `{"prompt":"do thing"}`)
		d.results = append(d.results, Result{Index: i, Output: "ok"})
	}
	tool := newTaskTool(d.run, nil)

	args := json.RawMessage(`{"tasks":[` + strings.Join(specs, ",") + `]}`)
	_, err := tool.Run(context.Background(), args)
	require.NoError(t, err)
	require.Equal(t, defaultTaskConcurrency, d.gotLimit,
		"a wide fan-out is capped at the default concurrency")
}

func TestTaskToolFewerTasksThanCapUsesTaskCount(t *testing.T) {
	d := &recordingDispatch{results: []Result{{Output: "ok"}}}
	tool := newTaskTool(d.run, nil)
	_, err := tool.Run(context.Background(), json.RawMessage(`{"tasks":[{"prompt":"p"}]}`))
	require.NoError(t, err)
	require.Equal(t, 1, d.gotLimit)
}

func TestTaskToolRejectsUnknownSubagentType(t *testing.T) {
	d := &recordingDispatch{}
	tool := newTaskTool(d.run, []string{"coder", "task"})
	res, err := tool.Run(context.Background(),
		json.RawMessage(`{"tasks":[{"prompt":"p","subagent_type":"ghost"}]}`))
	require.NoError(t, err)
	require.True(t, res.IsError)
	require.Contains(t, res.Content, "unknown subagent_type")
	require.Nil(t, d.gotTasks, "dispatch must not run when validation fails")
}

func TestTaskToolRejectsEmptyTasks(t *testing.T) {
	d := &recordingDispatch{}
	tool := newTaskTool(d.run, nil)
	res, err := tool.Run(context.Background(), json.RawMessage(`{"tasks":[]}`))
	require.NoError(t, err)
	require.True(t, res.IsError)
	require.Contains(t, res.Content, "at least one")
	require.Nil(t, d.gotTasks)
}

func TestTaskToolRejectsEmptyPrompt(t *testing.T) {
	d := &recordingDispatch{}
	tool := newTaskTool(d.run, nil)
	res, err := tool.Run(context.Background(),
		json.RawMessage(`{"tasks":[{"prompt":"   "}]}`))
	require.NoError(t, err)
	require.True(t, res.IsError)
	require.Contains(t, res.Content, "prompt is required")
	require.Nil(t, d.gotTasks)
}

func TestTaskToolRejectsInvalidJSON(t *testing.T) {
	d := &recordingDispatch{}
	tool := newTaskTool(d.run, nil)
	res, err := tool.Run(context.Background(), json.RawMessage(`{"tasks":`))
	require.NoError(t, err)
	require.True(t, res.IsError)
	require.Contains(t, res.Content, "invalid task arguments")
}

func TestTaskToolReportsPerTaskErrorInline(t *testing.T) {
	d := &recordingDispatch{
		results: []Result{
			{Index: 0, Output: "found it"},
			{Index: 1, Err: errors.New("provider exploded")},
		},
		err: errors.New("subtask 1: provider exploded"),
	}
	tool := newTaskTool(d.run, nil)
	res, err := tool.Run(context.Background(), json.RawMessage(`{"tasks":[
		{"description":"ok one","prompt":"a"},
		{"description":"bad one","prompt":"b"}
	]}`))
	require.NoError(t, err)
	// One success means the aggregate is not an error, but the failure shows inline.
	require.False(t, res.IsError)
	require.Contains(t, res.Content, "found it")
	require.Contains(t, res.Content, "error: provider exploded")
}

func TestTaskToolAllFailingMarksAggregateError(t *testing.T) {
	d := &recordingDispatch{results: []Result{
		{Index: 0, Err: errors.New("boom")},
	}}
	tool := newTaskTool(d.run, nil)
	res, err := tool.Run(context.Background(), json.RawMessage(`{"tasks":[{"prompt":"a"}]}`))
	require.NoError(t, err)
	require.True(t, res.IsError, "every subtask failing marks the whole result an error")
}

func TestTaskToolEmptyOutputRendersPlaceholder(t *testing.T) {
	d := &recordingDispatch{results: []Result{{Index: 0, Output: "   "}}}
	tool := newTaskTool(d.run, nil)
	res, err := tool.Run(context.Background(), json.RawMessage(`{"tasks":[{"prompt":"a"}]}`))
	require.NoError(t, err)
	require.False(t, res.IsError)
	require.Contains(t, res.Content, "(no output)")
}

func TestTaskToolTitleFallsBackToAgentName(t *testing.T) {
	d := &recordingDispatch{results: []Result{{Index: 0, Output: "done"}}}
	tool := newTaskTool(d.run, []string{"task"})
	// No description given, so the section header uses the agent name.
	res, err := tool.Run(context.Background(), json.RawMessage(`{"tasks":[{"prompt":"a"}]}`))
	require.NoError(t, err)
	require.Contains(t, res.Content, "## 1. task")
}

// TestTaskToolWiredToCoderNotTaskAgent verifies the Coordinator folds the task
// tool into the coder agent's callable tool set while the read-only "task"
// agent — whose allow-list excludes it — never sees it, so dispatched
// subagents cannot recurse into further dispatches.
func TestTaskToolWiredToCoderNotTaskAgent(t *testing.T) {
	coord := newDispatchCoordinator(t, &echoProvider{})

	coder, err := coord.Agent("coder")
	require.NoError(t, err)
	require.True(t, hasLLMTool(coder, taskToolName),
		"coder agent should be able to dispatch subagents")

	task, err := coord.Agent("task")
	require.NoError(t, err)
	require.False(t, hasLLMTool(task, taskToolName),
		"read-only task agent must not be able to recurse into the task tool")
}

func TestTaskToolDescriptionListsAgents(t *testing.T) {
	tool := newTaskTool(func(context.Context, []SubTask, int) ([]Result, error) { return nil, nil },
		[]string{"task", "coder", "task"})
	desc := tool.Description()
	// Names are deduplicated and sorted for a stable description.
	require.Contains(t, desc, "coder, task")
	require.Equal(t, []string{"coder", "task"}, tool.agents)

	var schema map[string]any
	require.NoError(t, json.Unmarshal(tool.Schema(), &schema))
	require.Equal(t, "object", schema["type"])
}
