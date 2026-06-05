// Package recipe provides a declarative, shareable task-template system for
// BharatCode. A recipe is a JSON file that captures a repeatable task as a
// parameterised prompt: it carries a title, a description, a set of typed
// parameters, and a prompt template. Callers load a recipe, supply runtime
// parameter values, and call Render to get back a final prompt string ready to
// seed an agent turn.
//
// # File format
//
// Recipes are stored as JSON files (*.recipe.json) and follow this structure:
//
//	{
//	  "title": "Generate unit tests",
//	  "description": "Scaffold table-driven Go tests for a named package.",
//	  "prompt": "Write table-driven tests for the package {{package}}. Coverage target: {{coverage}}.",
//	  "parameters": [
//	    {"name": "package",  "type": "string",  "requirement": "required",   "description": "Go import path of the package to test"},
//	    {"name": "coverage", "type": "string",  "requirement": "optional",   "default": "80%", "description": "Minimum coverage target"},
//	    {"name": "style",    "type": "select",  "requirement": "user_prompt","options": ["testify","stdlib"], "description": "Assertion style"}
//	  ],
//	  "extensions": ["bash", "edit"]
//	}
//
// # Placeholder syntax
//
// Prompt text uses {{paramName}} as the placeholder. Substitution is performed
// by a simple, safe string-replacement pass — no arbitrary code execution.
// Whitespace inside the braces is not trimmed; {{name}} and {{ name }} are
// different placeholders.
package recipe

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// ParamType enumerates the value shapes a Parameter may hold.
type ParamType string

const (
	// ParamTypeString accepts any string value.
	ParamTypeString ParamType = "string"
	// ParamTypeNumber accepts a string that represents a numeric value. The
	// recipe layer does not coerce the value — it is passed as a string to
	// the rendered prompt. Validation confirms it is non-empty when required.
	ParamTypeNumber ParamType = "number"
	// ParamTypeBool accepts "true" or "false" (case-insensitive).
	ParamTypeBool ParamType = "bool"
	// ParamTypeSelect accepts one of the values enumerated in Options.
	ParamTypeSelect ParamType = "select"
)

// Requirement describes how a parameter must be supplied by the caller.
type Requirement string

const (
	// RequirementRequired means the caller must supply a non-empty value.
	// Render returns an error when a required parameter is missing and has
	// no default.
	RequirementRequired Requirement = "required"
	// RequirementOptional means the parameter may be omitted; its default
	// value (which may be empty) is used when no value is provided.
	RequirementOptional Requirement = "optional"
	// RequirementUserPrompt is a hint that the TUI should interactively ask
	// the user for this value before rendering. From Render's perspective it
	// behaves identically to RequirementRequired — the caller is responsible
	// for supplying the value.
	RequirementUserPrompt Requirement = "user_prompt"
)

// Parameter describes one typed, named slot in a recipe's prompt template.
type Parameter struct {
	// Name is the identifier used in {{name}} placeholders.
	Name string `json:"name"`
	// Type constrains the kind of value the parameter accepts.
	Type ParamType `json:"type"`
	// Requirement controls whether callers must supply a value.
	Requirement Requirement `json:"requirement"`
	// Default is the value used when the caller does not supply one. It is
	// valid for any requirement level; for required parameters a non-empty
	// default satisfies the requirement when no value is passed.
	Default string `json:"default,omitempty"`
	// Description is a human-readable explanation shown in recipe listings.
	Description string `json:"description,omitempty"`
	// Options lists the accepted values for select parameters. It must be
	// non-empty when Type is ParamTypeSelect.
	Options []string `json:"options,omitempty"`
}

// Recipe is the in-memory representation of one recipe file. All fields map
// directly to the JSON keys of the same (snake_case) name.
type Recipe struct {
	// Title is a short, human-readable name for the recipe.
	Title string `json:"title"`
	// Description is a one-line summary suitable for listing pages.
	Description string `json:"description"`
	// Instructions is an optional free-form preamble that may accompany the
	// Prompt. When non-empty it is prepended to the rendered output separated
	// by a blank line.
	Instructions string `json:"instructions,omitempty"`
	// Prompt is the template string. Use {{paramName}} to reference a
	// parameter value. Render substitutes all recognised placeholders.
	Prompt string `json:"prompt"`
	// Parameters declares the typed slots available for substitution.
	Parameters []Parameter `json:"parameters,omitempty"`
	// Extensions lists the tool or extension names the agent should enable
	// while executing this recipe's rendered prompt. It is advisory; the
	// runtime wiring decides whether to honour it.
	Extensions []string `json:"extensions,omitempty"`
}

// validParamTypes is the set of accepted ParamType values for Validate.
var validParamTypes = map[ParamType]struct{}{
	ParamTypeString: {},
	ParamTypeNumber: {},
	ParamTypeBool:   {},
	ParamTypeSelect: {},
}

// validRequirements is the set of accepted Requirement values for Validate.
var validRequirements = map[Requirement]struct{}{
	RequirementRequired:   {},
	RequirementOptional:   {},
	RequirementUserPrompt: {},
}

// Load parses a recipe from the JSON file at path and returns the Recipe. It
// returns an error when the file cannot be read or when the JSON is malformed.
// It does not run Validate — call Validate separately when you need
// semantic checks.
func Load(path string) (*Recipe, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading recipe %s: %w", path, err)
	}
	var r Recipe
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parsing recipe %s: %w", path, err)
	}
	return &r, nil
}

// Validate checks that r is semantically valid: Title and Prompt must be
// non-empty; every parameter must have a non-empty Name and a recognised Type
// and Requirement; select parameters must declare at least one option; and no
// two parameters may share the same name. It returns the first error found, or
// nil when the recipe is well-formed.
func Validate(r *Recipe) error {
	if strings.TrimSpace(r.Title) == "" {
		return fmt.Errorf("recipe: title is required")
	}
	if strings.TrimSpace(r.Prompt) == "" {
		return fmt.Errorf("recipe %q: prompt is required", r.Title)
	}
	seen := make(map[string]struct{}, len(r.Parameters))
	for i, p := range r.Parameters {
		if strings.TrimSpace(p.Name) == "" {
			return fmt.Errorf("recipe %q: parameter[%d] name is required", r.Title, i)
		}
		if _, ok := validParamTypes[p.Type]; !ok {
			return fmt.Errorf("recipe %q: parameter %q has unknown type %q (want string|number|bool|select)", r.Title, p.Name, p.Type)
		}
		if _, ok := validRequirements[p.Requirement]; !ok {
			return fmt.Errorf("recipe %q: parameter %q has unknown requirement %q (want required|optional|user_prompt)", r.Title, p.Name, p.Requirement)
		}
		if p.Type == ParamTypeSelect && len(p.Options) == 0 {
			return fmt.Errorf("recipe %q: select parameter %q must declare at least one option", r.Title, p.Name)
		}
		if _, dup := seen[p.Name]; dup {
			return fmt.Errorf("recipe %q: duplicate parameter name %q", r.Title, p.Name)
		}
		seen[p.Name] = struct{}{}
	}
	return nil
}

// Render substitutes parameter values into r.Prompt (and r.Instructions, when
// non-empty) and returns the final prompt text ready to seed an agent turn.
//
// For each parameter: if a value is present in params it is used; otherwise
// the parameter's Default is used. A parameter that is RequirementRequired (or
// RequirementUserPrompt) with no supplied value and no Default causes Render to
// return an error naming the missing parameter.
//
// For ParamTypeBool the supplied or default value must be "true" or "false"
// (case-insensitive); any other value is rejected with an error.
//
// For ParamTypeSelect the supplied or default value must be one of the
// parameter's Options; any other value is rejected.
//
// After all substitutions, Render returns an error if any {{...}} placeholder
// remains in the text, because that indicates a placeholder that does not
// correspond to any declared parameter.
func Render(r *Recipe, params map[string]string) (string, error) {
	// Build a name→Parameter index for O(1) lookup.
	byName := make(map[string]Parameter, len(r.Parameters))
	for _, p := range r.Parameters {
		byName[p.Name] = p
	}

	// Resolve each parameter to its effective value.
	resolved := make(map[string]string, len(r.Parameters))
	for _, p := range r.Parameters {
		val, supplied := params[p.Name]
		if !supplied || val == "" {
			val = p.Default
		}
		// Check that required parameters are satisfied.
		if val == "" && (p.Requirement == RequirementRequired || p.Requirement == RequirementUserPrompt) {
			return "", fmt.Errorf("recipe %q: required parameter %q is missing and has no default", r.Title, p.Name)
		}
		// Type-level validation for bool.
		if val != "" && p.Type == ParamTypeBool {
			lower := strings.ToLower(val)
			if lower != "true" && lower != "false" {
				return "", fmt.Errorf("recipe %q: bool parameter %q must be \"true\" or \"false\", got %q", r.Title, p.Name, val)
			}
		}
		// Type-level validation for select.
		if val != "" && p.Type == ParamTypeSelect {
			if !containsString(p.Options, val) {
				return "", fmt.Errorf("recipe %q: select parameter %q value %q is not one of %v", r.Title, p.Name, val, p.Options)
			}
		}
		resolved[p.Name] = val
	}

	prompt, err := substitute(r.Prompt, resolved)
	if err != nil {
		return "", fmt.Errorf("recipe %q prompt: %w", r.Title, err)
	}

	if strings.TrimSpace(r.Instructions) == "" {
		return prompt, nil
	}

	instructions, err := substitute(r.Instructions, resolved)
	if err != nil {
		return "", fmt.Errorf("recipe %q instructions: %w", r.Title, err)
	}
	return instructions + "\n\n" + prompt, nil
}

// substitute replaces every {{name}} occurrence in text with the value from
// resolved[name]. It returns an error when any {{...}} placeholder remains
// after substitution, indicating an undeclared parameter.
func substitute(text string, resolved map[string]string) (string, error) {
	// Replace all known placeholders.
	for name, val := range resolved {
		text = strings.ReplaceAll(text, "{{"+name+"}}", val)
	}
	// Detect remaining placeholders.
	if idx := strings.Index(text, "{{"); idx >= 0 {
		end := strings.Index(text[idx:], "}}")
		placeholder := text[idx:]
		if end >= 0 {
			placeholder = text[idx : idx+end+2]
		}
		return "", fmt.Errorf("unresolved placeholder %s (no matching parameter declared)", placeholder)
	}
	return text, nil
}

// containsString reports whether slice contains s.
func containsString(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
