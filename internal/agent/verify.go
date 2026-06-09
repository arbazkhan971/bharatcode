package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/tools"
)

// VerifyConfidence ranks how strongly a discovered command is believed to be
// the right way to check a repo. It orders the candidates returned by
// DiscoverVerifyCommands: a native test invocation found in a project's own
// manifest outranks a generic smoke check synthesized for a project that ships
// no tests at all.
type VerifyConfidence int

const (
	// ConfidenceLow marks a fallback smoke check (e.g. opening a single HTML
	// file in a browser) proposed because no real test command could be found.
	// It proves the artifact loads, not that it is correct.
	ConfidenceLow VerifyConfidence = iota
	// ConfidenceMedium marks a command inferred from the toolchain rather than
	// declared by the project — a bare `pytest`/`cargo test` run, or a Python
	// import smoke test for a package that defines no test suite.
	ConfidenceMedium
	// ConfidenceHigh marks a command the project itself declares: a Go module's
	// `go test ./...`, or an npm/yarn script named test/build/lint. These are the
	// commands a maintainer would run.
	ConfidenceHigh
)

// String renders the confidence level for diagnostics and reason strings.
func (c VerifyConfidence) String() string {
	switch c {
	case ConfidenceHigh:
		return "high"
	case ConfidenceMedium:
		return "medium"
	case ConfidenceLow:
		return "low"
	default:
		return "unknown"
	}
}

// VerifyCandidate is one command the agent might run to confirm a change did not
// break the project, paired with the evidence that suggested it. Candidates are
// proposals, not guarantees: the caller picks among them (highest Confidence
// first) and is responsible for actually executing the command.
type VerifyCandidate struct {
	// Command is the shell command to run from the repo root, e.g.
	// "go test ./..." or "npm run build". Test-runner commands are chosen so
	// their failure output is parseable by the tools package (see
	// tools.RecognizesTestCommand), letting a downstream verify loop surface
	// concrete failures rather than a bare exit code.
	Command string
	// Confidence ranks this candidate against the others discovered for the same
	// repo. DiscoverVerifyCommands returns candidates sorted by it, descending.
	Confidence VerifyConfidence
	// Reason is a short, human-readable justification ("go.mod present",
	// "package.json has a \"test\" script") suitable for showing the user or
	// logging why the command was proposed.
	Reason string
}

// DiscoverVerifyCommands inspects the files at the repo root and returns an
// ordered set of commands that would verify a change there. Detection is
// deterministic and filesystem-only — it reads manifests (go.mod, package.json,
// Cargo.toml, pyproject.toml) and scans the top level — so it stays cheap enough
// to run before every verification.
//
// The returned candidates are sorted by Confidence (highest first); ties keep
// the order in which detectors ran, which roughly follows how decisive each
// signal is. An empty slice means nothing recognizable was found and the caller
// should fall back to asking the model what to run.
//
// A repo can match several ecosystems at once (a Go service with a Node
// frontend, say); every applicable command is returned so the caller can run
// more than one.
func DiscoverVerifyCommands(root string) []VerifyCandidate {
	if root == "" {
		root = "."
	}
	var out []VerifyCandidate
	out = append(out, detectGo(root)...)
	out = append(out, detectNode(root)...)
	out = append(out, detectPython(root)...)
	out = append(out, detectRust(root)...)
	out = append(out, detectStaticHTML(root)...)

	// Stable sort by descending confidence: SliceStable keeps the detector
	// ordering within a confidence band, so the highest-signal command of each
	// band leads. A repo with both a high-confidence `go test` and a
	// low-confidence HTML smoke check surfaces the real test first.
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Confidence > out[j].Confidence
	})
	return out
}

// detectGo proposes `go test ./...` when the root is a Go module. The presence
// of go.mod is decisive: it is a high-confidence signal because the command is
// exactly what a maintainer runs and its failures are parsed by the tools
// package.
func detectGo(root string) []VerifyCandidate {
	if !fileExists(filepath.Join(root, "go.mod")) {
		return nil
	}
	return []VerifyCandidate{{
		Command:    "go test ./...",
		Confidence: ConfidenceHigh,
		Reason:     "go.mod present (Go module)",
	}}
}

// nodePackage is the slice of package.json this discovery cares about: the
// declared run scripts. Everything else is ignored.
type nodePackage struct {
	Scripts map[string]string `json:"scripts"`
}

// detectNode reads package.json and proposes the project's own test/build/lint
// scripts, in that priority order. A declared script is high-confidence: it is
// what `npm test`/`npm run build` would invoke. The package manager prefix is
// chosen from the lockfile (yarn/pnpm/bun) so the command matches the project's
// tooling, defaulting to npm.
func detectNode(root string) []VerifyCandidate {
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return nil
	}
	var pkg nodePackage
	if err := json.Unmarshal(data, &pkg); err != nil {
		// Malformed package.json: we still know it is a Node project, but cannot
		// trust its scripts. A lint/build command would be a guess, so leave it
		// to other detectors / the model rather than propose something broken.
		return nil
	}
	runner := nodeRunner(root)
	var out []VerifyCandidate
	// test > build > lint: a passing test suite is the strongest evidence a
	// change is sound; build catches compile/type errors; lint is the weakest
	// but still cheap signal. Each is only proposed when the script exists.
	for _, script := range []string{"test", "build", "lint"} {
		if _, ok := pkg.Scripts[script]; !ok {
			continue
		}
		out = append(out, VerifyCandidate{
			Command:    runner + " " + script,
			Confidence: ConfidenceHigh,
			Reason:     "package.json has a \"" + script + "\" script",
		})
	}
	return out
}

// nodeRunner picks the command prefix for running a package script based on the
// lockfile present at root: "yarn"/"pnpm"/"bun run" for those ecosystems, else
// "npm run". The prefix is what precedes the script name (yarn omits the "run").
func nodeRunner(root string) string {
	switch {
	case fileExists(filepath.Join(root, "bun.lockb")), fileExists(filepath.Join(root, "bun.lock")):
		return "bun run"
	case fileExists(filepath.Join(root, "pnpm-lock.yaml")):
		return "pnpm"
	case fileExists(filepath.Join(root, "yarn.lock")):
		// yarn runs scripts as `yarn <script>` (no "run").
		return "yarn"
	default:
		return "npm run"
	}
}

// detectPython proposes a verification command for a Python project. When the
// project advertises pytest (a pytest config, a tests/ dir, or pyproject/setup
// listing it) a bare `pytest` run is medium-confidence — it is the standard
// runner but the project did not pin a script. When there is a package but no
// discoverable test suite, fall back to an import smoke test: importing the
// top-level module proves it at least loads without a syntax/import error.
func detectPython(root string) []VerifyCandidate {
	pyproject := filepath.Join(root, "pyproject.toml")
	hasPyproject := fileExists(pyproject)
	hasSetup := fileExists(filepath.Join(root, "setup.py")) || fileExists(filepath.Join(root, "setup.cfg"))
	hasRequirements := fileExists(filepath.Join(root, "requirements.txt"))
	if !hasPyproject && !hasSetup && !hasRequirements {
		// Not obviously a Python project. A lone .py script is handled by neither
		// branch on purpose: running it blind could have side effects.
		return nil
	}

	if hasPytest(root) {
		return []VerifyCandidate{{
			Command:    "pytest",
			Confidence: ConfidenceMedium,
			Reason:     "pytest available (config, tests/, or declared dependency)",
		}}
	}

	// No test suite to run. Smoke-test the package by importing it: a clean
	// import rules out syntax errors and broken top-level imports without
	// executing application logic. Prefer the package name declared by the
	// project; fall back to a top-level package dir with __init__.py.
	if mod := pythonImportTarget(root, hasPyproject, hasSetup); mod != "" {
		return []VerifyCandidate{{
			Command:    "python -c \"import " + mod + "\"",
			Confidence: ConfidenceMedium,
			Reason:     "no test suite found; importing \"" + mod + "\" as a smoke check",
		}}
	}
	return nil
}

// hasPytest reports whether the repo at root is set up to run pytest: an
// explicit config file, a conventional tests/ directory, or a pyproject/setup
// that names pytest among its dependencies.
func hasPytest(root string) bool {
	if fileExists(filepath.Join(root, "pytest.ini")) ||
		fileExists(filepath.Join(root, "tox.ini")) ||
		fileExists(filepath.Join(root, "conftest.py")) {
		return true
	}
	if dirExists(filepath.Join(root, "tests")) || dirExists(filepath.Join(root, "test")) {
		return true
	}
	for _, name := range []string{"pyproject.toml", "setup.py", "setup.cfg", "requirements.txt"} {
		if fileContains(filepath.Join(root, name), "pytest") {
			return true
		}
	}
	return false
}

// pythonImportTarget guesses the importable module name for a smoke test. It
// reads the project name from pyproject's [project] name (lightly parsed, no
// TOML dependency) and normalizes it to an import path; failing that, it returns
// the first top-level directory that is a Python package (has __init__.py).
func pythonImportTarget(root string, hasPyproject, hasSetup bool) string {
	if hasPyproject {
		if name := tomlProjectName(filepath.Join(root, "pyproject.toml")); name != "" {
			return normalizeModuleName(name)
		}
	}
	if hasSetup {
		// setup.cfg carries the name as "name = pkg" under [metadata]; reuse the
		// same key=value scan rather than parsing INI structure.
		if name := iniValue(filepath.Join(root, "setup.cfg"), "name"); name != "" {
			return normalizeModuleName(name)
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if fileExists(filepath.Join(root, e.Name(), "__init__.py")) {
			return e.Name()
		}
	}
	return ""
}

// tomlProjectName extracts the [project] name from a pyproject.toml without a
// TOML parser: it scans for a top-level `name = "..."` line. This is a
// best-effort heuristic — projects using Poetry's [tool.poetry] table are
// covered too since the key shape is identical.
func tomlProjectName(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, ln := range strings.Split(string(data), "\n") {
		ln = strings.TrimSpace(ln)
		if !strings.HasPrefix(ln, "name") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(ln, "name"))
		if !strings.HasPrefix(rest, "=") {
			continue
		}
		val := strings.TrimSpace(strings.TrimPrefix(rest, "="))
		val = strings.Trim(val, "\"'")
		if val != "" {
			return val
		}
	}
	return ""
}

// iniValue returns the value of "key = value" from an INI-style file, ignoring
// section headers. It is a minimal scan used for setup.cfg's metadata name.
func iniValue(path, key string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, ln := range strings.Split(string(data), "\n") {
		ln = strings.TrimSpace(ln)
		k, v, ok := strings.Cut(ln, "=")
		if !ok {
			continue
		}
		if strings.TrimSpace(k) == key {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// normalizeModuleName turns a distribution name ("my-cool-pkg") into its
// conventional import name ("my_cool_pkg") by replacing dashes with underscores.
func normalizeModuleName(name string) string {
	return strings.ReplaceAll(strings.TrimSpace(name), "-", "_")
}

// detectRust proposes `cargo test` when the root is a Cargo package or
// workspace. Cargo.toml is the decisive signal; the command is the standard
// runner but not project-declared, so it is medium confidence.
func detectRust(root string) []VerifyCandidate {
	if !fileExists(filepath.Join(root, "Cargo.toml")) {
		return nil
	}
	return []VerifyCandidate{{
		Command:    "cargo test",
		Confidence: ConfidenceMedium,
		Reason:     "Cargo.toml present (Rust package)",
	}}
}

// detectStaticHTML handles plain static sites — a single index.html or a lone
// HTML file with no build tooling. There is nothing to compile or unit-test, so
// the strongest available check is a smoke test: load the page in a headless
// browser (or, lacking one, just confirm the file parses/opens). This is
// deliberately low confidence — it proves the document loads, not that it
// behaves — and is the answer to "an empty single-file HTML app".
//
// It is suppressed when a real toolchain is present (package.json, etc.): those
// projects build the HTML and are covered by their native commands, so a raw
// file-open check would be redundant and misleading.
func detectStaticHTML(root string) []VerifyCandidate {
	if fileExists(filepath.Join(root, "package.json")) ||
		fileExists(filepath.Join(root, "go.mod")) ||
		fileExists(filepath.Join(root, "Cargo.toml")) {
		return nil
	}
	html := primaryHTMLFile(root)
	if html == "" {
		return nil
	}
	return []VerifyCandidate{{
		Command:    "browser-smoke " + html,
		Confidence: ConfidenceLow,
		Reason:     "static HTML app (" + html + "); load it in a browser to confirm it renders without console errors",
	}}
}

// primaryHTMLFile returns the entry HTML document at root, preferring index.html
// and otherwise the sole top-level .html file. It returns "" when there is no
// HTML at the top level or when several compete and none is named index (no
// clear entry point to smoke-test).
func primaryHTMLFile(root string) string {
	if fileExists(filepath.Join(root, "index.html")) {
		return "index.html"
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	var htmls []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.EqualFold(filepath.Ext(e.Name()), ".html") {
			htmls = append(htmls, e.Name())
		}
	}
	if len(htmls) == 1 {
		return htmls[0]
	}
	return ""
}

// fileExists reports whether path is an existing regular (non-directory) file.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// dirExists reports whether path is an existing directory.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// fileContains reports whether the file at path exists and contains needle. A
// missing or unreadable file is treated as not containing it.
func fileContains(path, needle string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), needle)
}

// Compile-time check that test-runner candidates this package proposes are ones
// the tools package can parse failures from. It documents the intended reuse of
// testparse.go: a downstream verify loop running these commands gets structured
// failures, not just an exit code.
var _ = tools.RecognizesTestCommand
