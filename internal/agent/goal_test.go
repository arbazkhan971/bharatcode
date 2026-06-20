package agent

import (
	"context"
	"errors"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"

	"github.com/arbazkhan971/bharatcode/internal/goal"
	"github.com/arbazkhan971/bharatcode/internal/message"
)

// textResult builds a minimal *fantasy.AgentResult whose
// Response.Content.Text() returns text.
func textResult(text string) *fantasy.AgentResult {
	return &fantasy.AgentResult{
		Response: fantasy.Response{
			Content: fantasy.ResponseContent{
				fantasy.TextContent{Text: text},
			},
		},
	}
}

// fakeGoalRunner is a scripted GoalTurnRunner. Each call returns the next
// reply/err in sequence and records the prompt it was given.
type fakeGoalRunner struct {
	replies []string
	errs    []error

	calls   int
	prompts []string
}

func (f *fakeGoalRunner) Run(_ context.Context, _ string, prompt string, _ ...message.Attachment) (*fantasy.AgentResult, error) {
	i := f.calls
	f.calls++
	f.prompts = append(f.prompts, prompt)
	if i < len(f.errs) && f.errs[i] != nil {
		return nil, f.errs[i]
	}
	var text string
	if i < len(f.replies) {
		text = f.replies[i]
	}
	return textResult(text), nil
}

func TestRunToGoal(t *testing.T) {
	boom := errors.New("provider exploded")

	tests := []struct {
		name          string
		replies       []string
		errs          []error
		maxIterations int
		wantOutcome   goal.Outcome
		wantIters     int
		wantLast      string
		wantErr       error
	}{
		{
			name:          "achieved on iter 2",
			replies:       []string{"made some progress", "everything verified.\nGOAL_COMPLETE"},
			maxIterations: 5,
			wantOutcome:   goal.Achieved,
			wantIters:     2,
			wantLast:      "everything verified.",
		},
		{
			name:          "blocked on iter 1",
			replies:       []string{"GOAL_BLOCKED need database credentials"},
			maxIterations: 5,
			wantOutcome:   goal.Blocked,
			wantIters:     1,
			wantLast:      "need database credentials",
		},
		{
			name:          "max iterations reached",
			replies:       []string{"step a", "step b", "step c"},
			maxIterations: 3,
			wantOutcome:   goal.MaxIterations,
			wantIters:     3,
			wantLast:      "step c",
		},
		{
			name:          "stalled on identical text",
			replies:       []string{"same output", "same output"},
			maxIterations: 5,
			wantOutcome:   goal.Stalled,
			wantIters:     2,
			wantLast:      "same output",
		},
		{
			name:          "runner error",
			errs:          []error{boom},
			maxIterations: 5,
			wantOutcome:   goal.Errored,
			wantIters:     1,
			wantLast:      "",
			wantErr:       boom,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &fakeGoalRunner{replies: tt.replies, errs: tt.errs}
			res, err := RunToGoal(context.Background(), runner, "sess-1", "do the thing", tt.maxIterations, nil)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tt.wantOutcome, res.Outcome)
			require.Equal(t, tt.wantIters, res.Iterations)
			require.Equal(t, tt.wantLast, res.LastMessage)
		})
	}
}

func TestRunToGoalUsesKickoffThenContinue(t *testing.T) {
	runner := &fakeGoalRunner{replies: []string{"a", "b", "done\nGOAL_COMPLETE"}}
	var seen []int
	res, err := RunToGoal(context.Background(), runner, "sess", "the goal text", 5, func(iter int, _ string) {
		seen = append(seen, iter)
	})
	require.NoError(t, err)
	require.Equal(t, goal.Achieved, res.Outcome)
	require.Equal(t, []int{1, 2, 3}, seen)

	require.Len(t, runner.prompts, 3)
	// First prompt is the kickoff, later prompts are continuations.
	require.Equal(t, goal.KickoffPrompt("the goal text"), runner.prompts[0])
	require.Equal(t, goal.ContinuePrompt("the goal text"), runner.prompts[1])
	require.Equal(t, goal.ContinuePrompt("the goal text"), runner.prompts[2])
}

func TestRunToGoalDefaultsMaxIterations(t *testing.T) {
	// No replies => every turn returns empty text, which never stalls (empty
	// text is ignored) and never completes, so the run exhausts the default
	// iteration budget.
	runner := &fakeGoalRunner{}
	res, err := RunToGoal(context.Background(), runner, "sess", "goal", 0, nil)
	require.NoError(t, err)
	require.Equal(t, goal.MaxIterations, res.Outcome)
	require.Equal(t, goal.DefaultMaxIterations, res.Iterations)
	require.Equal(t, goal.DefaultMaxIterations, runner.calls)
}

func TestRunToGoalCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner := &fakeGoalRunner{errs: []error{context.Canceled}}
	res, err := RunToGoal(ctx, runner, "sess", "goal", 5, nil)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, goal.Cancelled, res.Outcome)
	require.Equal(t, 1, res.Iterations)
}
