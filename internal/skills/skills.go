// Package skills implements a model-discoverable capability framework.
//
// A skill is a directory containing a SKILL.md file with YAML-ish
// frontmatter (name, description) followed by a Markdown body of
// instructions. Skills live under a skills root directory — one
// subdirectory per skill — so the agent can advertise the available
// skills in its system prompt and load a skill's body on demand,
// keeping the base prompt lean.
package skills

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// skillFilename is the manifest file that marks a directory as a skill.
const skillFilename = "SKILL.md"

// Skill is a single discoverable capability package.
type Skill struct {
	// Name is the skill identifier from the frontmatter.
	Name string
	// Description is the one-line summary from the frontmatter.
	Description string
	// Body is the Markdown instruction text following the frontmatter.
	Body string
	// Dir is the absolute path of the skill's directory.
	Dir string
}

// SkillSet is an immutable collection of loaded skills, keyed by name
// and ordered deterministically.
type SkillSet struct {
	byName map[string]Skill
	order  []string
}

// LoadSkills loads every skill found under the given root directories.
// Each root is a skills/ directory whose immediate subdirectories are
// individual skills; a subdirectory is a skill when it contains a
// SKILL.md. Roots are scanned in order, so a later root overrides an
// earlier one when both define a skill of the same name (project roots
// passed after global roots thus win). A missing root is skipped, and a
// malformed skill directory is skipped with a warning rather than
// failing the whole load. The returned error is reserved for
// unrecoverable filesystem failures.
func LoadSkills(dirs ...string) (*SkillSet, error) {
	set := &SkillSet{byName: make(map[string]Skill)}
	for _, root := range dirs {
		if strings.TrimSpace(root) == "" {
			continue
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("reading skills root %s: %w", root, err)
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			dir := filepath.Join(root, entry.Name())
			skill, ok := loadSkill(dir)
			if !ok {
				continue
			}
			set.put(skill)
		}
	}
	set.finalize()
	return set, nil
}

// put inserts or replaces a skill by name. Replacement preserves no
// ordering state because finalize rebuilds the order from byName.
func (s *SkillSet) put(skill Skill) {
	s.byName[skill.Name] = skill
}

// finalize rebuilds the deterministic name order from the current
// contents so List and Summaries are stable across runs.
func (s *SkillSet) finalize() {
	s.order = make([]string, 0, len(s.byName))
	for name := range s.byName {
		s.order = append(s.order, name)
	}
	sort.Strings(s.order)
}

// loadSkill reads and parses the SKILL.md in dir. It returns false when
// the directory is not a skill (no SKILL.md) or is malformed (missing a
// name or description); malformed directories are logged and skipped.
func loadSkill(dir string) (Skill, bool) {
	path := filepath.Join(dir, skillFilename)
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("Skipping skill: cannot read manifest", "dir", dir, "error", err)
		}
		return Skill{}, false
	}
	name, description, body, err := parseSkill(string(data))
	if err != nil {
		slog.Warn("Skipping malformed skill", "dir", dir, "error", err)
		return Skill{}, false
	}
	return Skill{
		Name:        name,
		Description: description,
		Body:        body,
		Dir:         dir,
	}, true
}

// parseSkill parses SKILL.md content into its name, description, and
// body. The content must open with a "---" delimited frontmatter block
// holding at least name: and description: keys. It returns an error
// when the frontmatter is absent, unterminated, or missing a required
// key.
func parseSkill(content string) (name, description, body string, err error) {
	rest, ok := strings.CutPrefix(content, "---\n")
	if !ok {
		// Tolerate a leading byte-order mark or stray blank lines.
		trimmed := strings.TrimLeft(content, "\ufeff \t\r\n")
		rest, ok = strings.CutPrefix(trimmed, "---\n")
		if !ok {
			return "", "", "", fmt.Errorf("missing frontmatter delimiter")
		}
	}
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", "", "", fmt.Errorf("unterminated frontmatter")
	}
	frontmatter := rest[:end]
	body = rest[end+len("\n---"):]
	// Drop the remainder of the closing delimiter line and any leading
	// blank lines before the body proper.
	if nl := strings.IndexByte(body, '\n'); nl >= 0 {
		body = body[nl+1:]
	} else {
		body = ""
	}
	body = strings.TrimSpace(body)

	for _, line := range strings.Split(frontmatter, "\n") {
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		switch strings.ToLower(key) {
		case "name":
			name = value
		case "description":
			description = value
		}
	}

	if name == "" {
		return "", "", "", fmt.Errorf("frontmatter missing name")
	}
	if description == "" {
		return "", "", "", fmt.Errorf("frontmatter missing description")
	}
	return name, description, body, nil
}

// List returns the loaded skills in deterministic name order.
func (s *SkillSet) List() []Skill {
	if s == nil {
		return nil
	}
	out := make([]Skill, 0, len(s.order))
	for _, name := range s.order {
		out = append(out, s.byName[name])
	}
	return out
}

// Get returns the named skill and whether it was found.
func (s *SkillSet) Get(name string) (Skill, bool) {
	if s == nil {
		return Skill{}, false
	}
	skill, ok := s.byName[name]
	return skill, ok
}

// Len reports how many skills the set holds.
func (s *SkillSet) Len() int {
	if s == nil {
		return 0
	}
	return len(s.byName)
}

// Summaries renders the loaded skills as an <available_skills> XML block
// for injection into the system prompt. Each skill becomes a <skill>
// element carrying its <name>, <description>, and the absolute <location>
// of its directory, in deterministic name order. Advertising the
// location lets the model load a skill's manifest by absolute path and
// resolve any relative paths the skill references against that directory.
// It returns an empty string when the set holds no skills.
func (s *SkillSet) Summaries() string {
	if s == nil || len(s.order) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<available_skills>")
	for _, name := range s.order {
		skill := s.byName[name]
		b.WriteString("\n  <skill>")
		b.WriteString("\n    <name>")
		b.WriteString(escapeXML(skill.Name))
		b.WriteString("</name>")
		b.WriteString("\n    <description>")
		b.WriteString(escapeXML(skill.Description))
		b.WriteString("</description>")
		b.WriteString("\n    <location>")
		b.WriteString(escapeXML(skill.Dir))
		b.WriteString("</location>")
		b.WriteString("\n  </skill>")
	}
	b.WriteString("\n</available_skills>")
	return b.String()
}

// escapeXML escapes the five XML special characters so skill metadata
// that contains markup characters renders as well-formed XML text rather
// than confusing the model's parse of the block.
func escapeXML(s string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return replacer.Replace(s)
}
