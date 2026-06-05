package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// promptExtension is the file extension, including the leading dot,
// that a file must have to be registered as a custom prompt.
const promptExtension = ".md"

// promptInputVar is the placeholder name reserved for the remaining
// slash arguments. A prompt template may reference {{input}} to splice
// in whatever text follows the prompt name on the invoking slash line.
const promptInputVar = "input"

// placeholderPattern matches a single {{var}} placeholder, capturing
// the variable name. Names are restricted to word characters so that
// surrounding braces or stray text do not greedily match across
// multiple placeholders.
var placeholderPattern = regexp.MustCompile(`\{\{(\w+)\}\}`)

// dollarArgPattern matches the pi-style positional argument placeholders a
// slash prompt may use. In one alternation it captures: $$ (a literal dollar
// sign), $@ and $ARGUMENTS (the entire argument line), and $1, $2, ... (an
// individual, 1-indexed field of the argument line). Digits are matched
// greedily so $12 references the twelfth field rather than the first.
var dollarArgPattern = regexp.MustCompile(`\$(\$|@|ARGUMENTS|[0-9]+)`)

// Prompt is a single reusable Markdown prompt loaded from a registry
// directory. Its Name is the source filename with the .md extension
// stripped, and Template is the trimmed file body, which may contain
// {{var}} placeholders interpolated at render time.
//
// A prompt file may begin with an optional YAML-style frontmatter block,
// fenced by lines containing only "---". Recognised keys are description
// (a one-line summary shown in /help listings) and argument-hint (a short
// usage hint such as "<file> <concern>"). The frontmatter is stripped from
// the file before Template is computed, so placeholders never see it.
// Matching Claude Code / opencode custom-command conventions, the keys are
// case-insensitive and argument_hint is accepted as a synonym for
// argument-hint. Unknown keys are ignored.
type Prompt struct {
	// Name is the invokable prompt name, e.g. "triage" for triage.md.
	Name string `json:"name"`
	// Template is the trimmed Markdown body with {{var}} placeholders.
	Template string `json:"template"`
	// Description is the optional one-line summary from frontmatter, shown in
	// command listings. It is empty when the file declares no description.
	Description string `json:"description,omitempty"`
	// ArgumentHint is the optional usage hint from frontmatter, e.g.
	// "<file> <concern>". It is empty when the file declares none.
	ArgumentHint string `json:"argument_hint,omitempty"`
	// Source is the absolute path of the file the prompt was loaded from.
	Source string `json:"source"`
}

// PromptRegistry holds the set of custom Markdown prompts discovered
// across one or more directories, keyed by prompt name. When the same
// name appears in multiple directories, the prompt from the
// later-listed directory wins, matching the convention that more
// specific (e.g. project-local) sources override more general (e.g.
// global) ones.
type PromptRegistry struct {
	prompts map[string]Prompt
}

// LoadPromptRegistry builds a PromptRegistry from the given directories.
// Directories are scanned in order; each *.md file becomes a prompt
// named after the file with its extension removed. When a name is
// defined in more than one directory, the prompt from the directory
// listed later overrides the earlier one. A directory that does not
// exist is skipped silently rather than treated as an error, since the
// global and project prompt directories are commonly absent.
func LoadPromptRegistry(dirs ...string) (*PromptRegistry, error) {
	reg := &PromptRegistry{prompts: make(map[string]Prompt)}
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		if err := reg.loadDir(dir); err != nil {
			return nil, err
		}
	}
	return reg, nil
}

// loadDir scans a single directory for *.md prompt files and merges
// them into the registry. A missing directory is not an error: it is
// skipped so callers can pass optional global and project paths
// unconditionally. Non-.md files and subdirectories are ignored.
func (r *PromptRegistry) loadDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading prompts directory %s: %w", dir, err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.EqualFold(filepath.Ext(name), promptExtension) {
			continue
		}
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading prompt file %s: %w", path, err)
		}
		promptName := strings.TrimSuffix(name, filepath.Ext(name))
		meta, body := parseFrontmatter(string(data))
		hint := meta["argument-hint"]
		if hint == "" {
			hint = meta["argument_hint"]
		}
		r.prompts[promptName] = Prompt{
			Name:         promptName,
			Template:     strings.TrimSpace(body),
			Description:  meta["description"],
			ArgumentHint: hint,
			Source:       path,
		}
	}
	return nil
}

// Names returns the registered prompt names in sorted order. The
// result is a fresh slice that the caller may modify freely.
func (r *PromptRegistry) Names() []string {
	names := make([]string, 0, len(r.prompts))
	for name := range r.prompts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Get returns the prompt registered under name and reports whether it
// exists.
func (r *PromptRegistry) Get(name string) (Prompt, bool) {
	p, ok := r.prompts[name]
	return p, ok
}

// List returns every registered prompt, including its frontmatter metadata,
// sorted by Name. The result is a fresh slice the caller may modify freely; it
// lets command listings present custom prompts with their descriptions and
// argument hints.
func (r *PromptRegistry) List() []Prompt {
	out := make([]Prompt, 0, len(r.prompts))
	for _, p := range r.prompts {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// parseFrontmatter splits an optional leading YAML-style frontmatter block from
// the prompt body. The block must start at the very beginning of the file
// (after an optional UTF-8 BOM) with a line containing only "---" and end at
// the next such line; its interior is parsed as case-insensitive "key: value"
// pairs with surrounding quotes stripped from values. When no well-formed block
// is present the whole input is returned as the body and meta is nil, so a
// document that merely contains a "---" horizontal rule mid-text is untouched.
func parseFrontmatter(raw string) (meta map[string]string, body string) {
	s := strings.TrimPrefix(raw, "\ufeff")
	lines := strings.Split(s, "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], "\r") != "---" {
		return nil, raw
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], "\r") == "---" {
			end = i
			break
		}
	}
	if end == -1 {
		// No closing fence: treat the whole file as body to avoid swallowing it.
		return nil, raw
	}
	meta = make(map[string]string)
	for _, line := range lines[1:end] {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "" {
			continue
		}
		meta[key] = unquoteValue(strings.TrimSpace(value))
	}
	return meta, strings.Join(lines[end+1:], "\n")
}

// unquoteValue strips a single pair of matching surrounding single or double
// quotes from a frontmatter value, leaving unquoted values untouched.
func unquoteValue(v string) string {
	if len(v) >= 2 {
		if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
			return v[1 : len(v)-1]
		}
	}
	return v
}

// Render looks up the named prompt and interpolates its {{var}}
// placeholders using args. Each placeholder is replaced by the value of
// the matching key; {{input}} is treated like any other key and is
// expected to hold the remaining slash arguments. Extra keys in args
// that the template does not reference are ignored. It returns an error
// when name is not registered, or when the template references a
// placeholder that args does not supply.
func (r *PromptRegistry) Render(name string, args map[string]string) (string, error) {
	prompt, ok := r.prompts[name]
	if !ok {
		return "", fmt.Errorf("rendering prompt %q: %w", name, ErrPromptNotFound)
	}
	return renderTemplate(prompt.Template, args)
}

// ErrPromptNotFound is returned by Render when the requested prompt
// name is not registered.
var ErrPromptNotFound = fmt.Errorf("prompt not found")

// RenderSlash renders the named prompt the way a slash invocation supplies
// arguments: a single argLine following the prompt name (e.g. the
// "flaky test in CI" in "/triage flaky test in CI"). Before the {{var}}
// placeholders are interpolated, the template's pi-style positional
// placeholders are expanded from argLine:
//
//	$1, $2, ...     individual fields of argLine, split on whitespace while
//	               honoring single and double quotes (1-indexed; an
//	               out-of-range index expands to the empty string)
//	$@, $ARGUMENTS the entire argLine, verbatim
//	$$             a literal dollar sign
//
// For backward compatibility the {{input}} placeholder is also populated with
// the full argLine, so templates may freely mix the two styles. RenderSlash
// returns ErrPromptNotFound when name is unregistered, mirroring Render.
func (r *PromptRegistry) RenderSlash(name, argLine string) (string, error) {
	prompt, ok := r.prompts[name]
	if !ok {
		return "", fmt.Errorf("rendering prompt %q: %w", name, ErrPromptNotFound)
	}
	expanded := expandDollarArgs(prompt.Template, argLine)
	return renderTemplate(expanded, map[string]string{promptInputVar: argLine})
}

// expandDollarArgs substitutes the pi-style positional placeholders in
// template using argLine. See RenderSlash for the placeholder grammar. The
// argument line is split into fields once and shared across all positional
// references; unmatched indices expand to the empty string so a template that
// optimistically references $3 degrades cleanly when fewer args are supplied.
func expandDollarArgs(template, argLine string) string {
	fields := splitFields(argLine)
	return dollarArgPattern.ReplaceAllStringFunc(template, func(match string) string {
		// match always begins with '$'; the remainder selects the placeholder.
		switch token := match[1:]; token {
		case "$":
			return "$"
		case "@", "ARGUMENTS":
			return argLine
		default:
			// token is one or more digits per the pattern, so Atoi cannot fail.
			n, _ := strconv.Atoi(token)
			if n >= 1 && n <= len(fields) {
				return fields[n-1]
			}
			return ""
		}
	})
}

// splitFields splits s into fields on unquoted whitespace, honoring single
// (') and double (") quotes so a quoted run containing spaces stays one
// field. Quote characters are removed from the result, and an unterminated
// quote extends to the end of the input. It is intentionally a small parser:
// it does not interpret backslash escapes, keeping positional-argument
// splitting predictable for slash prompts.
func splitFields(s string) []string {
	var fields []string
	var cur strings.Builder
	inField := false
	var quote rune // the active quote rune, or 0 when outside a quoted run
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
			inField = true
		case unicode.IsSpace(r):
			if inField {
				fields = append(fields, cur.String())
				cur.Reset()
				inField = false
			}
		default:
			cur.WriteRune(r)
			inField = true
		}
	}
	if inField {
		fields = append(fields, cur.String())
	}
	return fields
}

// renderTemplate replaces every {{var}} placeholder in template with
// the corresponding value from args. A placeholder whose variable is
// absent from args produces an error naming the missing variable; this
// surfaces typos and forgotten arguments rather than silently emitting
// the raw placeholder. A single pass over the template both substitutes
// known variables and detects the first missing one.
func renderTemplate(template string, args map[string]string) (string, error) {
	var missing string
	out := placeholderPattern.ReplaceAllStringFunc(template, func(match string) string {
		// match is the full "{{name}}"; recover the captured name.
		varName := placeholderPattern.FindStringSubmatch(match)[1]
		if value, ok := args[varName]; ok {
			return value
		}
		if missing == "" {
			missing = varName
		}
		return match
	})
	if missing != "" {
		return "", fmt.Errorf("rendering prompt: missing value for variable %q", missing)
	}
	return out, nil
}
