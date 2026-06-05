package tui

import (
	"fmt"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/recipe"
	"github.com/arbazkhan971/bharatcode/internal/tui/dialog"
	"github.com/arbazkhan971/bharatcode/internal/tui/styles"
	tea "github.com/charmbracelet/bubbletea/v2"
)

// recipeParamDialog is a modal dialog that collects a single recipe parameter
// value from the user. It is pushed onto the dialog stack; when the user
// submits (enter) or cancels (esc) the dialog pops and the result fields are
// read by the model's handleKey before it returns, allowing the recipe
// collection sequence to advance to the next parameter.
type recipeParamDialog struct {
	theme    styles.Theme
	param    recipe.Parameter
	inputBuf strings.Builder
	// result holds the submitted value (set before pop=true on enter).
	result string
	// cancelled is true when the user pressed esc instead of enter.
	cancelled bool
}

// ID returns a stable dialog id incorporating the parameter name.
func (d *recipeParamDialog) ID() string {
	return "recipe_param_" + d.param.Name
}

// Render returns the dialog body for this parameter's collection prompt.
func (d *recipeParamDialog) Render(width int) string {
	var lines []string
	lines = append(lines, fmt.Sprintf("Parameter: %s", d.param.Name))
	if d.param.Description != "" {
		lines = append(lines, d.param.Description)
	}
	typeHint := string(d.param.Type)
	if d.param.Type == recipe.ParamTypeSelect && len(d.param.Options) > 0 {
		typeHint += " [" + strings.Join(d.param.Options, "|") + "]"
	}
	lines = append(lines, "Type: "+typeHint)
	if d.param.Default != "" {
		lines = append(lines, "Default: "+d.param.Default)
	}
	lines = append(lines, "")
	lines = append(lines, "> "+d.inputBuf.String()+"▌")
	lines = append(lines, "")
	lines = append(lines, "enter to confirm · esc to cancel recipe")
	body := strings.Join(lines, "\n")
	if width > 8 {
		var clamped []string
		for _, ln := range strings.Split(body, "\n") {
			r := []rune(ln)
			if len(r) > width-4 {
				ln = string(r[:width-4])
			}
			clamped = append(clamped, ln)
		}
		body = strings.Join(clamped, "\n")
	}
	return d.theme.Modal.Render(body)
}

// HandleKey processes keystrokes. Printable text is appended to the buffer;
// backspace removes the last rune; enter submits (falling back to Default when
// the buffer is empty); esc cancels. Always returns handled=true.
func (d *recipeParamDialog) HandleKey(msg tea.KeyPressMsg) (handled bool, pop bool) {
	switch msg.String() {
	case "enter":
		d.result = d.inputBuf.String()
		if d.result == "" {
			d.result = d.param.Default
		}
		d.cancelled = false
		return true, true
	case "esc":
		d.cancelled = true
		return true, true
	case "backspace":
		s := d.inputBuf.String()
		if s != "" {
			r := []rune(s)
			d.inputBuf.Reset()
			d.inputBuf.WriteString(string(r[:len(r)-1]))
		}
		return true, false
	default:
		if msg.Key().Text != "" {
			d.inputBuf.WriteString(msg.Key().Text)
		}
		return true, false
	}
}

// recipeParamCollector drives sequential interactive collection of all
// RequirementUserPrompt parameters for a recipe. It is stored on the model
// (m.recipeCollector) between key events so handleKey can advance it after
// each dialog pops.
type recipeParamCollector struct {
	pending    []recipe.Parameter // remaining user_prompt params (not yet collected)
	collected  map[string]string  // param values gathered so far
	onComplete func(map[string]string) (tea.Model, tea.Cmd)
}

// newRecipeParamCollector builds a collector for the user_prompt parameters of
// recipe r that are not already satisfied by prePopulated.
func newRecipeParamCollector(
	_ *model,
	r *recipe.Recipe,
	prePopulated map[string]string,
	onComplete func(map[string]string) (tea.Model, tea.Cmd),
) *recipeParamCollector {
	var pending []recipe.Parameter
	for _, p := range r.Parameters {
		if p.Requirement != recipe.RequirementUserPrompt {
			continue
		}
		if _, ok := prePopulated[p.Name]; ok {
			continue
		}
		pending = append(pending, p)
	}
	collected := make(map[string]string, len(prePopulated))
	for k, v := range prePopulated {
		collected[k] = v
	}
	return &recipeParamCollector{
		pending:    pending,
		collected:  collected,
		onComplete: onComplete,
	}
}

// pushNextOrComplete pushes the dialog for the next pending parameter onto the
// model's dialog stack, or calls onComplete when all parameters have been
// collected. It returns whatever (model, cmd) the call produces.
func (c *recipeParamCollector) pushNextOrComplete(m *model) (tea.Model, tea.Cmd) {
	if len(c.pending) == 0 {
		m.recipeCollector = nil // done; clear the model field
		return c.onComplete(c.collected)
	}
	param := c.pending[0]
	c.pending = c.pending[1:]
	m.dialogs.Push(&recipeParamDialog{
		theme: m.theme,
		param: param,
	})
	return m, nil
}

// advanceFromDialog is called by the model's handleKey after a
// recipeParamDialog pops from the stack. It records the submitted value and
// advances to the next parameter (or completes).
func (c *recipeParamCollector) advanceFromDialog(m *model, paramName, value string, cancelled bool) (tea.Model, tea.Cmd) {
	if cancelled {
		m.recipeCollector = nil
		m.dialogs.Push(&dialog.Text{
			DialogID: "recipe_cancelled",
			Title:    "Recipe cancelled",
			Body:     "Parameter collection cancelled.",
			Theme:    m.theme,
		})
		return m, nil
	}
	c.collected[paramName] = value
	return c.pushNextOrComplete(m)
}
