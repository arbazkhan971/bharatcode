package agent

import (
	"context"
	"strings"

	"charm.land/fantasy"

	"github.com/arbazkhan971/bharatcode/internal/goal"
	"github.com/arbazkhan971/bharatcode/internal/message"
)

// GoalTurnRunner is the minimal seam RunToGoal needs to drive one full agentic
// turn. The Coordinator satisfies this interface.
type GoalTurnRunner interface {
	Run(ctx context.Context, sessionID, prompt string, attachments ...message.Attachment) (*fantasy.AgentResult, error)
}

// RunToGoal drives the runner across multiple turns until the agent signals
// completion, signals it is blocked, stops making progress, the iteration
// budget is exhausted, the context is cancelled, or a turn errors.
//
// onIteration, when non-nil, is called after each turn with the 1-based
// iteration number and the trimmed final text of that turn.
func RunToGoal(
	ctx context.Context,
	runner GoalTurnRunner,
	sessionID, goalText string,
	maxIterations int,
	onIteration func(iter int, text string),
) (goal.Result, error) {
	if maxIterations <= 0 {
		maxIterations = goal.DefaultMaxIterations
	}

	var prevText string
	for i := 1; i <= maxIterations; i++ {
		var prompt string
		if i == 1 {
			prompt = goal.KickoffPrompt(goalText)
		} else {
			prompt = goal.ContinuePrompt(goalText)
		}

		res, err := runner.Run(ctx, sessionID, prompt)
		if err != nil {
			if ctx.Err() != nil {
				return goal.Result{Outcome: goal.Cancelled, Iterations: i, LastMessage: prevText}, ctx.Err()
			}
			return goal.Result{Outcome: goal.Errored, Iterations: i, LastMessage: ""}, err
		}

		text := strings.TrimSpace(res.Response.Content.Text())
		if onIteration != nil {
			onIteration(i, text)
		}

		if goal.IsComplete(text) {
			return goal.Result{Outcome: goal.Achieved, Iterations: i, LastMessage: goal.Strip(text)}, nil
		}
		if goal.IsBlocked(text) {
			return goal.Result{Outcome: goal.Blocked, Iterations: i, LastMessage: goal.Strip(text)}, nil
		}
		if text != "" && text == prevText {
			return goal.Result{Outcome: goal.Stalled, Iterations: i, LastMessage: text}, nil
		}
		prevText = text
	}

	return goal.Result{Outcome: goal.MaxIterations, Iterations: maxIterations, LastMessage: prevText}, nil
}
