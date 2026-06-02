package tui

import (
	"fmt"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/tui/dialog"
	tea "github.com/charmbracelet/bubbletea/v2"
)

// maxGoalIterations bounds the autonomous /goal run loop so it always
// terminates even if the agent never signals completion.
const maxGoalIterations = 10

// goalContinuePrompt is fed to the agent after each turn to drive the next
// iteration toward the active goal.
const goalContinuePrompt = "Continue toward the goal. If the goal is already fully met, reply with exactly GOAL_COMPLETE and nothing else."

// goalDoneMarker is the sentinel the agent emits to signal the goal is met,
// which stops the autonomous loop before the iteration cap.
const goalDoneMarker = "GOAL_COMPLETE"

// startGoal begins bounded autonomous iteration toward the active goal. It
// seeds the first run with the goal text and returns its command.
func (m *model) startGoal() (tea.Model, tea.Cmd) {
	if m.goal == "" {
		m.dialogs.Push(&dialog.Text{DialogID: "goal", Title: "Goal", Body: "No active goal. Set one with /goal <text>.", Theme: m.theme})
		return m, nil
	}
	if m.goalActive {
		m.dialogs.Push(&dialog.Text{DialogID: "goal", Title: "Goal", Body: "Goal loop already running.", Theme: m.theme})
		return m, nil
	}
	m.goalActive = true
	m.goalIteration = 1
	m.status.Goal = m.goalStatus()
	prompt := fmt.Sprintf("Goal: %s\n\nWork toward this goal. When it is fully met, reply with exactly %s.", m.goal, goalDoneMarker)
	return m.startRun(prompt)
}

// stopGoal halts the autonomous loop without clearing the stored goal text.
func (m *model) stopGoal() {
	m.goalActive = false
	m.goalIteration = 0
	m.status.Goal = ""
}

// advanceGoal decides whether to continue the autonomous loop after a turn
// finishes. It returns a command driving the next iteration, or nil when the
// loop is inactive, the goal is met, or the iteration cap is reached.
func (m *model) advanceGoal(last *message.Message) tea.Cmd {
	if !m.goalActive {
		return nil
	}
	if goalSignalledComplete(last) {
		m.stopGoal()
		m.dialogs.Push(&dialog.Text{DialogID: "goal", Title: "Goal complete", Body: m.goal, Theme: m.theme})
		return nil
	}
	if m.goalIteration >= maxGoalIterations {
		m.stopGoal()
		m.dialogs.Push(&dialog.Text{
			DialogID: "goal",
			Title:    "Goal stopped",
			Body:     fmt.Sprintf("Reached iteration cap (%d) without completion.", maxGoalIterations),
			Theme:    m.theme,
		})
		return nil
	}
	m.goalIteration++
	m.status.Goal = m.goalStatus()
	return m.continueRun(goalContinuePrompt)
}

// goalStatus renders the iteration progress shown in the status bar.
func (m *model) goalStatus() string {
	if !m.goalActive {
		return ""
	}
	return fmt.Sprintf("goal %d/%d", m.goalIteration, maxGoalIterations)
}

// goalSignalledComplete reports whether the assistant's last message contains
// the goal-completion marker.
func goalSignalledComplete(last *message.Message) bool {
	if last == nil {
		return false
	}
	return strings.Contains(assistantText(last), goalDoneMarker)
}
