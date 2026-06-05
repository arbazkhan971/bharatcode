package eval

import (
	"encoding/json"

	"github.com/arbazkhan971/bharatcode/internal/llm"
)

// Task describes one evaluation scenario: a fixture repo the runner sets up,
// the agent prompt that drives the task, and a checker that decides pass/fail.
type Task struct {
	// ID is the stable short identifier used in reports (e.g. "syntax-fix").
	ID string
	// Name is a human-readable task title.
	Name string
	// Goal is the user prompt sent to the agent.
	Goal string
	// Fixture builds the initial repo state for this task in a temp directory.
	Fixture FixtureBuilder
	// Script is the scripted provider turn sequence the stub provider replays.
	// Each inner slice is one provider turn: a sequence of llm.Events that the
	// stub streams back before closing the channel.
	Script [][]llm.Event
	// Check decides whether the task passed given the outcome. When nil, the
	// task passes whenever the agent run completes without error.
	Check CheckFn
}

// FixtureBuilder writes the initial repo state into dir.
type FixtureBuilder func(dir string) error

// CheckFn inspects the post-run repo state and returns (passed, reason).
type CheckFn func(dir string, outcome Outcome) (passed bool, reason string)

// Outcome carries the observable results of one agent run.
type Outcome struct {
	// ToolCalls is the ordered list of tool calls the agent made.
	ToolCalls []ToolCall
	// FinalText is the last assistant text message the agent produced.
	FinalText string
	// Err is non-nil when the agent run itself returned an error.
	Err error
}

// ToolCall records one tool invocation observed during the run.
type ToolCall struct {
	Name  string
	Input json.RawMessage
}

// -------- built-in tasks --------

// SyntaxErrorTask asks the agent to fix a Go file with a missing import.
func SyntaxErrorTask() Task {
	return Task{
		ID:      "syntax-fix",
		Name:    "Fix Go syntax error (missing import)",
		Goal:    "The file main.go has a syntax error — fix it so it compiles.",
		Fixture: fixtureSyntaxError,
		Script: [][]llm.Event{
			// Turn 1: agent reads the file.
			{
				toolUseEnd("call-1", "view", `{"path":"main.go"}`),
				endEvent(10, 5),
			},
			// Turn 2: agent edits the file.
			{
				toolUseEnd("call-2", "edit", `{"path":"main.go","old_string":"","new_string":"import \"fmt\"\n"}`),
				endEvent(12, 6),
			},
			// Turn 3: agent reports success.
			{
				deltaText("The syntax error is fixed; main.go now imports fmt correctly."),
				endEvent(8, 4),
			},
		},
		Check: func(dir string, out Outcome) (bool, string) {
			hasView := containsToolCall(out.ToolCalls, "view")
			hasEdit := containsToolCall(out.ToolCalls, "edit")
			if !hasView {
				return false, "agent never called the view tool"
			}
			if !hasEdit {
				return false, "agent never called the edit tool"
			}
			return true, "agent read then edited the file"
		},
	}
}

// MissingFunctionTask asks the agent to add a missing stub function.
func MissingFunctionTask() Task {
	return Task{
		ID:      "missing-func",
		Name:    "Add missing function stub",
		Goal:    "The file util.go calls helper() but the function is not defined — add a stub.",
		Fixture: fixtureMissingFunc,
		Script: [][]llm.Event{
			// Turn 1: agent reads util.go.
			{
				toolUseEnd("call-1", "view", `{"path":"util.go"}`),
				endEvent(10, 5),
			},
			// Turn 2: agent writes the stub.
			{
				toolUseEnd("call-2", "write", `{"path":"helper.go","content":"package main\n\nfunc helper() {}\n"}`),
				endEvent(12, 6),
			},
			// Turn 3: agent confirms.
			{
				deltaText("Added helper() stub in helper.go."),
				endEvent(8, 4),
			},
		},
		Check: func(dir string, out Outcome) (bool, string) {
			if !containsToolCall(out.ToolCalls, "write") && !containsToolCall(out.ToolCalls, "edit") {
				return false, "agent made no file write/edit"
			}
			return true, "agent wrote or edited a file"
		},
	}
}

// UpdateCommentTask asks the agent to update a stale comment.
func UpdateCommentTask() Task {
	return Task{
		ID:      "update-comment",
		Name:    "Update stale comment",
		Goal:    "The comment in README.go is outdated — update it to match the new function signature.",
		Fixture: fixtureUpdateComment,
		Script: [][]llm.Event{
			// Turn 1: read the file.
			{
				toolUseEnd("call-1", "view", `{"path":"README.go"}`),
				endEvent(10, 5),
			},
			// Turn 2: edit the comment.
			{
				toolUseEnd("call-2", "edit", `{"path":"README.go","old_string":"// oldComment","new_string":"// newComment"}`),
				endEvent(12, 6),
			},
			// Turn 3: done.
			{
				deltaText("Comment updated."),
				endEvent(8, 4),
			},
		},
		Check: func(dir string, out Outcome) (bool, string) {
			if !containsToolCall(out.ToolCalls, "edit") {
				return false, "agent never called the edit tool"
			}
			return true, "agent called edit"
		},
	}
}

// AddReturnValueTask asks the agent to add a return value to a function.
func AddReturnValueTask() Task {
	return Task{
		ID:      "add-return",
		Name:    "Add return value to function",
		Goal:    "calc.go has a function that should return an int but currently returns nothing — fix it.",
		Fixture: fixtureAddReturn,
		Script: [][]llm.Event{
			{
				toolUseEnd("call-1", "view", `{"path":"calc.go"}`),
				endEvent(10, 5),
			},
			{
				toolUseEnd("call-2", "edit", `{"path":"calc.go","old_string":"func add(a, b int) {","new_string":"func add(a, b int) int {"}`),
				endEvent(12, 6),
			},
			{
				deltaText("Return type added to add()."),
				endEvent(8, 4),
			},
		},
		Check: func(dir string, out Outcome) (bool, string) {
			if !containsToolCall(out.ToolCalls, "edit") {
				return false, "agent never called edit"
			}
			return true, "agent edited the file"
		},
	}
}

// FixOffByOneTask asks the agent to correct an off-by-one index bug.
func FixOffByOneTask() Task {
	return Task{
		ID:      "off-by-one",
		Name:    "Fix off-by-one index error",
		Goal:    "slice.go accesses index n instead of n-1 — fix the off-by-one bug.",
		Fixture: fixtureOffByOne,
		Script: [][]llm.Event{
			{
				toolUseEnd("call-1", "view", `{"path":"slice.go"}`),
				endEvent(10, 5),
			},
			{
				toolUseEnd("call-2", "edit", `{"path":"slice.go","old_string":"s[n]","new_string":"s[n-1]"}`),
				endEvent(12, 6),
			},
			{
				deltaText("Off-by-one fixed."),
				endEvent(8, 4),
			},
		},
		Check: func(dir string, out Outcome) (bool, string) {
			if !containsToolCall(out.ToolCalls, "edit") {
				return false, "agent never called edit"
			}
			return true, "agent edited the file"
		},
	}
}

// -------- helpers --------

func containsToolCall(calls []ToolCall, name string) bool {
	for _, c := range calls {
		if c.Name == name {
			return true
		}
	}
	return false
}

func toolUseEnd(id, name, input string) llm.Event {
	return llm.ToolUseEndEvent{ID: id, Name: name, Input: json.RawMessage(input)}
}

func deltaText(text string) llm.Event {
	return llm.DeltaTextEvent{Text: text}
}

func endEvent(in, out int) llm.Event {
	return llm.EndEvent{Usage: llm.Usage{InputTokens: in, OutputTokens: out}}
}
