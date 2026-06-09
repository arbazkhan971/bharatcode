package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/tools"
	"github.com/stretchr/testify/require"
)

// writeFile creates path under dir (creating parent directories) with the given
// contents, failing the test on any I/O error. It keeps the table tests below
// focused on what files a repo contains rather than on error plumbing.
func writeFile(t *testing.T, dir, rel, contents string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(contents), 0o644))
}

// commandsOf collects the Command field of each candidate, preserving order, so
// tests can assert on the proposed commands without caring about reasons.
func commandsOf(cands []VerifyCandidate) []string {
	out := make([]string, len(cands))
	for i, c := range cands {
		out[i] = c.Command
	}
	return out
}

// findCommand returns the candidate whose Command equals cmd, or false. It lets
// tests assert on a specific candidate's confidence/reason in a repo that yields
// several.
func findCommand(cands []VerifyCandidate, cmd string) (VerifyCandidate, bool) {
	for _, c := range cands {
		if c.Command == cmd {
			return c, true
		}
	}
	return VerifyCandidate{}, false
}

func TestDiscoverVerifyCommandsGoModule(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/app\n\ngo 1.24\n")

	cands := DiscoverVerifyCommands(dir)
	require.Len(t, cands, 1)
	require.Equal(t, "go test ./...", cands[0].Command)
	require.Equal(t, ConfidenceHigh, cands[0].Confidence)
	require.Contains(t, cands[0].Reason, "go.mod")
}

func TestDiscoverVerifyCommandsNodeScripts(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", `{
		"name": "app",
		"scripts": {
			"build": "vite build",
			"test": "jest",
			"lint": "eslint .",
			"start": "node ."
		}
	}`)

	cands := DiscoverVerifyCommands(dir)
	// test > build > lint priority, and "start" is ignored.
	require.Equal(t, []string{"npm run test", "npm run build", "npm run lint"}, commandsOf(cands))
	for _, c := range cands {
		require.Equal(t, ConfidenceHigh, c.Confidence, c.Command)
	}
}

func TestDiscoverVerifyCommandsNodeRunnerFromLockfile(t *testing.T) {
	cases := []struct {
		name     string
		lockfile string
		want     string
	}{
		{"npm default", "", "npm run test"},
		{"yarn", "yarn.lock", "yarn test"},
		{"pnpm", "pnpm-lock.yaml", "pnpm test"},
		{"bun", "bun.lockb", "bun run test"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, "package.json", `{"scripts":{"test":"vitest run"}}`)
			if tc.lockfile != "" {
				writeFile(t, dir, tc.lockfile, "")
			}
			cands := DiscoverVerifyCommands(dir)
			require.Len(t, cands, 1)
			require.Equal(t, tc.want, cands[0].Command)
		})
	}
}

func TestDiscoverVerifyCommandsNodeNoScripts(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", `{"name":"app","dependencies":{"react":"^18"}}`)

	cands := DiscoverVerifyCommands(dir)
	require.Empty(t, cands, "no test/build/lint scripts means no Node candidate")
}

func TestDiscoverVerifyCommandsNodeMalformedJSON(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", `{ this is not json `)

	cands := DiscoverVerifyCommands(dir)
	require.Empty(t, cands, "a malformed package.json yields no guessed command")
}

func TestDiscoverVerifyCommandsPytestSignals(t *testing.T) {
	cases := []struct {
		name string
		// setup seeds the project files beyond the marker that makes it a Python
		// project; each case adds a different pytest signal.
		files map[string]string
		dirs  []string
	}{
		{
			name:  "pytest.ini",
			files: map[string]string{"pyproject.toml": "[project]\nname = \"app\"\n", "pytest.ini": "[pytest]\n"},
		},
		{
			name:  "tests dir",
			files: map[string]string{"setup.py": "from setuptools import setup\nsetup()\n"},
			dirs:  []string{"tests"},
		},
		{
			name:  "declared dependency",
			files: map[string]string{"requirements.txt": "requests==2.0\npytest==8.0\n"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for rel, contents := range tc.files {
				writeFile(t, dir, rel, contents)
			}
			for _, d := range tc.dirs {
				require.NoError(t, os.MkdirAll(filepath.Join(dir, d), 0o755))
			}
			cands := DiscoverVerifyCommands(dir)
			require.Len(t, cands, 1)
			require.Equal(t, "pytest", cands[0].Command)
			require.Equal(t, ConfidenceMedium, cands[0].Confidence)
		})
	}
}

func TestDiscoverVerifyCommandsPythonImportSmoke(t *testing.T) {
	dir := t.TempDir()
	// A package with no test suite of any kind: pyproject names it, and the
	// package directory exists, but there is no tests/ dir or pytest dependency.
	writeFile(t, dir, "pyproject.toml", "[project]\nname = \"my-cool-pkg\"\nversion = \"0.1.0\"\n")
	writeFile(t, dir, "my_cool_pkg/__init__.py", "")

	cands := DiscoverVerifyCommands(dir)
	require.Len(t, cands, 1)
	require.Equal(t, `python -c "import my_cool_pkg"`, cands[0].Command)
	require.Equal(t, ConfidenceMedium, cands[0].Confidence)
	require.Contains(t, cands[0].Reason, "smoke")
}

func TestDiscoverVerifyCommandsPythonImportFromPackageDir(t *testing.T) {
	dir := t.TempDir()
	// No name in pyproject, but a top-level package directory exists; the import
	// target is discovered from the directory with __init__.py.
	writeFile(t, dir, "requirements.txt", "requests\n")
	writeFile(t, dir, "widgets/__init__.py", "")

	cands := DiscoverVerifyCommands(dir)
	require.Len(t, cands, 1)
	require.Equal(t, `python -c "import widgets"`, cands[0].Command)
}

func TestDiscoverVerifyCommandsPythonNoModuleNoCandidate(t *testing.T) {
	dir := t.TempDir()
	// A requirements.txt marks it Python, but there is no test suite and no
	// importable package to smoke-test, so no command can be proposed.
	writeFile(t, dir, "requirements.txt", "requests\n")
	writeFile(t, dir, "notes.md", "# scratch\n")

	cands := DiscoverVerifyCommands(dir)
	require.Empty(t, cands)
}

func TestDiscoverVerifyCommandsRust(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Cargo.toml", "[package]\nname = \"app\"\nversion = \"0.1.0\"\n")

	cands := DiscoverVerifyCommands(dir)
	require.Len(t, cands, 1)
	require.Equal(t, "cargo test", cands[0].Command)
	require.Equal(t, ConfidenceMedium, cands[0].Confidence)
}

func TestDiscoverVerifyCommandsStaticHTMLIndex(t *testing.T) {
	dir := t.TempDir()
	// An empty single-file HTML app: just an index.html, no build tooling.
	writeFile(t, dir, "index.html", "<!doctype html><title>app</title>")

	cands := DiscoverVerifyCommands(dir)
	require.Len(t, cands, 1)
	require.Equal(t, "browser-smoke index.html", cands[0].Command)
	require.Equal(t, ConfidenceLow, cands[0].Confidence)
	require.Contains(t, cands[0].Reason, "static HTML")
}

func TestDiscoverVerifyCommandsStaticHTMLSingleNamedFile(t *testing.T) {
	dir := t.TempDir()
	// A lone, non-index HTML file is still a clear entry point.
	writeFile(t, dir, "game.html", "<!doctype html><title>tictactoe</title>")

	cands := DiscoverVerifyCommands(dir)
	require.Len(t, cands, 1)
	require.Equal(t, "browser-smoke game.html", cands[0].Command)
}

func TestDiscoverVerifyCommandsStaticHTMLAmbiguous(t *testing.T) {
	dir := t.TempDir()
	// Several HTML files and none named index: no single entry point to smoke.
	writeFile(t, dir, "a.html", "<html></html>")
	writeFile(t, dir, "b.html", "<html></html>")

	cands := DiscoverVerifyCommands(dir)
	require.Empty(t, cands)
}

func TestDiscoverVerifyCommandsStaticHTMLSuppressedByToolchain(t *testing.T) {
	dir := t.TempDir()
	// index.html is the build output of a Node app; the native script covers it,
	// so the raw file-open smoke check is suppressed.
	writeFile(t, dir, "index.html", "<html></html>")
	writeFile(t, dir, "package.json", `{"scripts":{"build":"vite build"}}`)

	cands := DiscoverVerifyCommands(dir)
	require.Equal(t, []string{"npm run build"}, commandsOf(cands))
	_, ok := findCommand(cands, "browser-smoke index.html")
	require.False(t, ok, "static HTML smoke check should be suppressed when a toolchain is present")
}

func TestDiscoverVerifyCommandsEmptyRepo(t *testing.T) {
	dir := t.TempDir()
	cands := DiscoverVerifyCommands(dir)
	require.Empty(t, cands, "nothing recognizable means no candidates")
}

func TestDiscoverVerifyCommandsPolyglotOrderedByConfidence(t *testing.T) {
	dir := t.TempDir()
	// A Go service (high) with a Rust component (medium) and a stray static page
	// — though the HTML check is suppressed by the toolchain. Verify ordering and
	// that suppression holds in a multi-ecosystem repo.
	writeFile(t, dir, "go.mod", "module example.com/app\n\ngo 1.24\n")
	writeFile(t, dir, "Cargo.toml", "[package]\nname = \"engine\"\n")
	writeFile(t, dir, "index.html", "<html></html>")

	cands := DiscoverVerifyCommands(dir)
	require.Equal(t, []string{"go test ./...", "cargo test"}, commandsOf(cands))
	// Highest confidence first.
	require.Equal(t, ConfidenceHigh, cands[0].Confidence)
	require.Equal(t, ConfidenceMedium, cands[1].Confidence)
}

func TestDiscoverVerifyCommandsDefaultsToCurrentDir(t *testing.T) {
	// An empty root string should not panic and should be treated as ".". Running
	// it from a temp dir keeps it deterministic regardless of the test's CWD
	// contents — we only assert it returns without error.
	require.NotPanics(t, func() { _ = DiscoverVerifyCommands("") })
}

// TestVerifyCandidateCommandsAreParseable asserts the reuse contract with the
// tools package: every test-runner command this discovery proposes is one whose
// failures tools can parse, so a downstream verify loop gets structured output.
// Non-test commands (build/lint, the HTML smoke check, the import smoke check)
// are intentionally excluded — they are not test runners.
func TestVerifyCandidateCommandsAreParseable(t *testing.T) {
	for _, cmd := range []string{"go test ./...", "npm run test", "yarn test", "pnpm test", "bun run test", "pytest", "cargo test"} {
		require.Truef(t, tools.RecognizesTestCommand(cmd),
			"tools package should parse failures from proposed test command %q", cmd)
	}
}

func TestVerifyConfidenceString(t *testing.T) {
	require.Equal(t, "high", ConfidenceHigh.String())
	require.Equal(t, "medium", ConfidenceMedium.String())
	require.Equal(t, "low", ConfidenceLow.String())
	require.Equal(t, "unknown", VerifyConfidence(99).String())
}
