package skills

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// namePattern is the grammar a skill name must satisfy: one or more
// lowercase ASCII letters, digits, or hyphens. The restricted set keeps
// names safe to use as path segments, command suffixes, and XML text.
var namePattern = regexp.MustCompile(`^[a-z0-9-]+$`)

// LoadedSkill is a skill discovered by the formal loader. It embeds the
// shared Skill type so existing renderers (Summaries and friends) keep
// working unchanged, and adds the manifest source path plus the
// frontmatter flags that the lightweight directory loader does not carry.
type LoadedSkill struct {
	Skill
	// Source is the absolute path of the manifest the skill was parsed
	// from. Unlike Dir it names the file, which matters when a directory
	// holds several *.md skill files.
	Source string
	// DisableModelInvocation records the optional frontmatter flag of the
	// same name. When true the skill is documented for humans but should
	// not be advertised to the model for autonomous invocation.
	DisableModelInvocation bool
}

// DiagnosticLevel classifies the severity of a Diagnostic.
type DiagnosticLevel int

const (
	// DiagWarn marks a recoverable problem: the offending manifest is
	// skipped but the load as a whole succeeds.
	DiagWarn DiagnosticLevel = iota
	// DiagError marks a manifest that is present but unusable, e.g. a
	// malformed name. It too is non-fatal to the overall load.
	DiagError
)

// String renders the level as a short lowercase token for logs and tests.
func (l DiagnosticLevel) String() string {
	switch l {
	case DiagWarn:
		return "warn"
	case DiagError:
		return "error"
	default:
		return "unknown"
	}
}

// Diagnostic reports a non-fatal problem with a single skill manifest.
// The loader accumulates Diagnostics rather than aborting so one bad
// manifest never hides every other skill in a tree.
type Diagnostic struct {
	// Path is the manifest the problem concerns, absolute when known.
	Path string
	// Level is the severity of the problem.
	Level DiagnosticLevel
	// Message describes the problem in a single human-readable line.
	Message string
}

// String renders a Diagnostic as "level: path: message".
func (d Diagnostic) String() string {
	return fmt.Sprintf("%s: %s: %s", d.Level, d.Path, d.Message)
}

// LoadSkillTree walks root recursively for skill manifests, parses each,
// and returns the valid skills together with non-fatal Diagnostics for
// any that are malformed. It complements the lightweight, single-level
// LoadSkills: where that scans one directory's immediate children and
// merges several roots into a SkillSet, this descends a whole tree, keeps
// each skill's source path, and surfaces malformed manifests as
// Diagnostics so callers can report them instead of swallowing a warning.
//
// A manifest is any file named SKILL.md or, more generally, any *.md
// file; a SKILL.md sitting directly in a directory names that directory's
// skill, and its frontmatter name must match the directory name. The
// returned skills are sorted by name. A missing root yields an empty
// result with no error; the error is reserved for an unreadable root or a
// walk failure that prevents discovery entirely.
//
// Common version-control noise is skipped cheaply: any path component
// named .git, plus the conventional hidden and vendored directories, are
// not descended into. This is a pragmatic, .gitignore-flavoured skip
// rather than a full ignore-file engine.
func LoadSkillTree(root string) ([]LoadedSkill, []Diagnostic, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, nil, nil
	}
	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("reading skills root %s: %w", root, err)
	}
	if !info.IsDir() {
		return nil, nil, fmt.Errorf("skills root %s is not a directory", root)
	}

	var (
		skills []LoadedSkill
		diags  []Diagnostic
	)
	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// Record the traversal hiccup and keep going; an unreadable
			// subtree should not sink the whole load.
			diags = append(diags, Diagnostic{Path: path, Level: DiagWarn, Message: fmt.Sprintf("cannot access: %v", err)})
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			// Never skip the root itself even if its own name matches a
			// skip rule; the user pointed us at it deliberately.
			if path != root && shouldSkipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !isManifest(d.Name()) {
			return nil
		}
		skill, diag, ok := loadManifest(path)
		if diag != nil {
			diags = append(diags, *diag)
		}
		if ok {
			skills = append(skills, skill)
		}
		return nil
	})
	if walkErr != nil {
		return nil, diags, fmt.Errorf("walking skills root %s: %w", root, walkErr)
	}

	sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
	return skills, diags, nil
}

// isManifest reports whether a file name denotes a skill manifest. Every
// Markdown file qualifies; SKILL.md is merely the canonical name.
func isManifest(name string) bool {
	return strings.EqualFold(filepath.Ext(name), ".md")
}

// isCanonicalManifest reports whether name is the canonical SKILL.md,
// which is the only manifest required to match its directory name.
func isCanonicalManifest(name string) bool {
	return strings.EqualFold(name, skillFilename)
}

// shouldSkipDir reports whether a directory of the given base name should
// be pruned from the walk. The set covers version-control metadata and
// the usual vendored or dependency directories so a skills tree checked
// out alongside code does not pay to descend into them.
func shouldSkipDir(name string) bool {
	switch name {
	case ".git", ".hg", ".svn", "node_modules", "vendor", ".cache":
		return true
	}
	return false
}

// loadManifest reads and parses a single manifest file. It returns the
// parsed skill and ok=true on success; on any problem it returns a
// populated Diagnostic and ok=false. A non-nil Diagnostic may accompany
// ok=true is never produced — a manifest either yields a skill or a
// diagnostic, not both.
func loadManifest(path string) (LoadedSkill, *Diagnostic, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return LoadedSkill{}, &Diagnostic{Path: path, Level: DiagWarn, Message: fmt.Sprintf("cannot read manifest: %v", err)}, false
	}
	meta, body, err := parseFrontmatter(string(data))
	if err != nil {
		return LoadedSkill{}, &Diagnostic{Path: path, Level: DiagWarn, Message: err.Error()}, false
	}

	name := meta["name"]
	if name == "" {
		return LoadedSkill{}, &Diagnostic{Path: path, Level: DiagWarn, Message: "frontmatter missing name"}, false
	}
	if !namePattern.MatchString(name) {
		return LoadedSkill{}, &Diagnostic{Path: path, Level: DiagError, Message: fmt.Sprintf("invalid name %q: must match [a-z0-9-]+", name)}, false
	}
	description := meta["description"]
	if description == "" {
		return LoadedSkill{}, &Diagnostic{Path: path, Level: DiagWarn, Message: "frontmatter missing description"}, false
	}

	dir := filepath.Dir(path)
	// A canonical SKILL.md identifies its directory, so its declared name
	// must agree with the directory's base name. Auxiliary *.md skills are
	// not bound to the directory name and may be named freely.
	if isCanonicalManifest(filepath.Base(path)) {
		if base := filepath.Base(dir); base != name {
			return LoadedSkill{}, &Diagnostic{Path: path, Level: DiagError, Message: fmt.Sprintf("name %q does not match directory %q", name, base)}, false
		}
	}

	disable, err := parseBool(meta["disable-model-invocation"])
	if err != nil {
		return LoadedSkill{}, &Diagnostic{Path: path, Level: DiagError, Message: fmt.Sprintf("invalid disable-model-invocation: %v", err)}, false
	}

	return LoadedSkill{
		Skill: Skill{
			Name:        name,
			Description: description,
			Body:        strings.TrimSpace(body),
			Dir:         dir,
		},
		Source:                 path,
		DisableModelInvocation: disable,
	}, nil, true
}

// parseFrontmatter splits a manifest into its frontmatter key/value map
// and Markdown body. The block is delimited by a leading "---" line and a
// matching "---" line; a leading byte-order mark and surrounding blank
// lines are tolerated. Keys are lowercased; values are trimmed and
// unquoted. It accepts both ':' (YAML-ish) and '=' (TOML-ish) as the
// key/value separator so either flavour of frontmatter parses.
func parseFrontmatter(content string) (map[string]string, string, error) {
	rest, ok := strings.CutPrefix(content, "---\n")
	if !ok {
		// Tolerate a leading byte-order mark or stray blank lines.
		trimmed := strings.TrimLeft(content, "\ufeff \t\r\n")
		rest, ok = strings.CutPrefix(trimmed, "---\n")
		if !ok {
			return nil, "", fmt.Errorf("missing frontmatter delimiter")
		}
	}
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return nil, "", fmt.Errorf("unterminated frontmatter")
	}
	frontmatter := rest[:end]
	body := rest[end+len("\n---"):]
	if nl := strings.IndexByte(body, '\n'); nl >= 0 {
		body = body[nl+1:]
	} else {
		body = ""
	}

	meta := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(frontmatter))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := splitKeyValue(line)
		if !found {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		if key != "" {
			meta[key] = value
		}
	}
	return meta, body, nil
}

// splitKeyValue splits a frontmatter line at the first ':' or '=',
// whichever comes first, so both YAML-ish and TOML-ish lines parse while
// any separator inside the value is preserved.
func splitKeyValue(line string) (key, value string, found bool) {
	colon := strings.IndexByte(line, ':')
	equals := strings.IndexByte(line, '=')
	idx := colon
	if colon < 0 || (equals >= 0 && equals < colon) {
		idx = equals
	}
	if idx < 0 {
		return "", "", false
	}
	return line[:idx], line[idx+1:], true
}

// parseBool interprets the optional boolean frontmatter values the loader
// understands. An empty string means the key was absent and yields false.
func parseBool(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return false, nil
	case "true", "yes", "1", "on":
		return true, nil
	case "false", "no", "0", "off":
		return false, nil
	default:
		return false, fmt.Errorf("not a boolean: %q", value)
	}
}
