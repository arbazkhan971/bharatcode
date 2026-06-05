package outputfilter

import (
	"regexp"
	"strings"
	"testing"
)

// TestFilterApplyNoMatch verifies that a filter returns (_, _, false) when the
// command does not match its MatchCommand pattern.
func TestFilterApplyNoMatch(t *testing.T) {
	eng := NewEngine()
	_, _, matched := eng.Apply("totally-unknown-command foo", "some output")
	if matched {
		t.Fatal("expected no match for unknown command, got match")
	}
}

// TestPipelineStageOrder checks that stages run in the documented order by
// constructing a minimal synthetic filter and verifying each stage's effect.
func TestPipelineStageOrder(t *testing.T) {
	f := &Filter{
		Name:         "synthetic",
		MatchCommand: re(`^synth\b`),
		Replace: []ReplaceRule{
			{Pattern: re(`REPLACE_ME`), Replacement: "REPLACED"},
		},
		StripLinesMatching: []*regexp.Regexp{
			re(`^noise`),
		},
		MaxLines: 3,
		OnEmpty:  "synthetic: ok",
	}

	input := "REPLACE_ME\nnoise line\nline1\nline2\nline3\nline4\n"
	result, ok := f.Apply("synth args", input)
	if !ok {
		t.Fatal("filter should have matched")
	}
	if !strings.Contains(result, "REPLACED") {
		t.Errorf("replace stage should have run; got: %q", result)
	}
	if strings.Contains(result, "REPLACE_ME") {
		t.Errorf("original string should not appear after replace; got: %q", result)
	}
	if strings.Contains(result, "noise line") {
		t.Errorf("strip_lines_matching should have removed 'noise line'; got: %q", result)
	}
	// max_lines=3: REPLACED + line1 + line2 = 3 kept, line3+line4 dropped → truncation notice
	if !strings.Contains(result, "more lines") {
		t.Errorf("max_lines cap should have added truncation notice; got: %q", result)
	}
}

// TestOnEmpty verifies that on_empty fires when all lines are stripped.
func TestOnEmpty(t *testing.T) {
	f := &Filter{
		Name:         "on-empty-test",
		MatchCommand: re(`^oe\b`),
		StripLinesMatching: []*regexp.Regexp{
			re(`.*`), // strip everything
		},
		OnEmpty: "on-empty: ok",
	}
	result, ok := f.Apply("oe args", "line1\nline2\n")
	if !ok {
		t.Fatal("filter should have matched")
	}
	if result != "on-empty: ok" {
		t.Errorf("expected on_empty message, got: %q", result)
	}
}

// TestMatchOutputShortCircuit verifies that a matching match_output rule returns
// the configured message without running later stages.
func TestMatchOutputShortCircuit(t *testing.T) {
	f := &Filter{
		Name:         "short-circuit-test",
		MatchCommand: re(`^sc\b`),
		MatchOutput: []MatchOutputRule{
			{Pattern: re(`up to date`), Message: "ok (up to date)"},
		},
		// These stages should not execute because match_output fires first.
		StripLinesMatching: []*regexp.Regexp{re(`.*`)},
		OnEmpty:            "SHOULD_NOT_APPEAR",
	}

	result, ok := f.Apply("sc install", "Resolving packages...\nAll packages up to date\n")
	if !ok {
		t.Fatal("filter should have matched")
	}
	if result != "ok (up to date)" {
		t.Errorf("expected short-circuit message, got: %q", result)
	}
}

// TestStripANSI verifies ANSI escape codes are removed before other stages.
func TestStripANSI(t *testing.T) {
	f := &Filter{
		Name:         "ansi-test",
		MatchCommand: re(`^ansitest\b`),
		StripANSI:    true,
	}
	input := "\x1b[32mGREEN TEXT\x1b[0m\n\x1b[1mBOLD\x1b[0m"
	result, ok := f.Apply("ansitest", input)
	if !ok {
		t.Fatal("filter should have matched")
	}
	if strings.Contains(result, "\x1b[") {
		t.Errorf("ANSI codes should have been stripped; got: %q", result)
	}
	if !strings.Contains(result, "GREEN TEXT") {
		t.Errorf("text content should be preserved after ANSI strip; got: %q", result)
	}
}

// TestTailLines verifies that tail_lines keeps only the last N lines.
func TestTailLines(t *testing.T) {
	f := &Filter{
		Name:         "tail-test",
		MatchCommand: re(`^tail\b`),
		TailLines:    2,
	}
	input := "line1\nline2\nline3\nline4\n"
	result, ok := f.Apply("tail cmd", input)
	if !ok {
		t.Fatal("filter should have matched")
	}
	if strings.Contains(result, "line1") || strings.Contains(result, "line2") {
		t.Errorf("tail_lines=2 should have dropped first lines; got: %q", result)
	}
	if !strings.Contains(result, "line3") || !strings.Contains(result, "line4") {
		t.Errorf("tail_lines=2 should have kept last two lines; got: %q", result)
	}
}

// TestTruncateLinesAt verifies that long lines are capped.
func TestTruncateLinesAt(t *testing.T) {
	f := &Filter{
		Name:            "truncline-test",
		MatchCommand:    re(`^longline\b`),
		TruncateLinesAt: 10,
	}
	input := "short\n" + strings.Repeat("x", 20) + "\nshort2"
	result, ok := f.Apply("longline", input)
	if !ok {
		t.Fatal("filter should have matched")
	}
	for _, line := range strings.Split(result, "\n") {
		if len(line) > 10 {
			t.Errorf("line exceeds TruncateLinesAt=10: %q (len=%d)", line, len(line))
		}
	}
}

// TestKeepLinesMatching verifies that keep_lines_matching drops non-matching lines.
func TestKeepLinesMatching(t *testing.T) {
	f := &Filter{
		Name:         "keep-test",
		MatchCommand: re(`^keep\b`),
		KeepLinesMatching: []*regexp.Regexp{
			re(`ERROR`),
			re(`FAIL`),
		},
	}
	input := "ok line\nERROR: something broke\nok line2\nFAIL: bad result\n"
	result, ok := f.Apply("keep cmd", input)
	if !ok {
		t.Fatal("filter should have matched")
	}
	if strings.Contains(result, "ok line") {
		t.Errorf("keep_lines_matching should have dropped 'ok line'; got: %q", result)
	}
	if !strings.Contains(result, "ERROR:") || !strings.Contains(result, "FAIL:") {
		t.Errorf("keep_lines_matching should have kept ERROR/FAIL lines; got: %q", result)
	}
}

// -----------------------------------------------------------------------------
// Builtin filter golden tests — port of rtk's inline TOML test style
// -----------------------------------------------------------------------------

// filterCase defines one input→expected pair for a builtin filter.
type filterCase struct {
	name     string
	cmd      string
	input    string
	expected string // empty string means "expect on_empty result"
}

func TestBuiltinMake(t *testing.T) {
	cases := []filterCase{
		{
			name:     "strips entering/leaving directory lines",
			cmd:      "make all",
			input:    "make[1]: Entering directory '/home/user'\ngcc -O2 foo.c\nmake[1]: Leaving directory '/home/user'\n",
			expected: "gcc -O2 foo.c",
		},
		{
			name:     "strips blank lines",
			cmd:      "make test",
			input:    "gcc -O2 foo.c\n\ngcc -O2 bar.c\n",
			expected: "gcc -O2 foo.c\ngcc -O2 bar.c",
		},
		{
			name:     "on_empty when all stripped",
			cmd:      "make",
			input:    "make[1]: Entering directory '/home/user'\nmake[1]: Leaving directory '/home/user'\n",
			expected: "make: ok",
		},
	}
	runBuiltinCases(t, cases)
}

func TestBuiltinGoBuild(t *testing.T) {
	cases := []filterCase{
		{
			name:     "preserves error lines",
			cmd:      "go build ./...",
			input:    "./cmd/main.go:10:5: undefined: Foo\n./cmd/main.go:15:2: undefined: Bar\n",
			expected: "./cmd/main.go:10:5: undefined: Foo\n./cmd/main.go:15:2: undefined: Bar",
		},
		{
			name:     "on_empty for successful build",
			cmd:      "go build ./...",
			input:    "\n",
			expected: "go build: ok",
		},
	}
	runBuiltinCases(t, cases)
}

func TestBuiltinGoTest(t *testing.T) {
	cases := []filterCase{
		{
			name: "strips RUN/PASS lines, keeps FAIL and summary",
			cmd:  "go test ./...",
			input: "=== RUN   TestFoo\n" +
				"--- PASS: TestFoo (0.00s)\n" +
				"=== RUN   TestBar\n" +
				"--- FAIL: TestBar (0.00s)\n" +
				"    bar_test.go:42: want 1, got 2\n" +
				"FAIL\tgithub.com/example/pkg\t0.005s\n",
			expected: "--- FAIL: TestBar (0.00s)\n    bar_test.go:42: want 1, got 2\nFAIL\tgithub.com/example/pkg\t0.005s",
		},
		{
			name:     "cached ok lines stripped, on_empty fires",
			cmd:      "go test ./...",
			input:    "ok  \tgithub.com/example/pkg\t(cached)\n",
			expected: "go test: all pass",
		},
	}
	runBuiltinCases(t, cases)
}

func TestBuiltinGoVet(t *testing.T) {
	cases := []filterCase{
		{
			name:     "preserves vet findings",
			cmd:      "go vet ./...",
			input:    "# github.com/example/pkg\n./foo.go:12:2: Printf format %d has arg x of wrong type float64\n",
			expected: "# github.com/example/pkg\n./foo.go:12:2: Printf format %d has arg x of wrong type float64",
		},
		{
			name:     "on_empty when no findings",
			cmd:      "go vet ./...",
			input:    "\n\n",
			expected: "go vet: ok",
		},
	}
	runBuiltinCases(t, cases)
}

func TestBuiltinGofmt(t *testing.T) {
	cases := []filterCase{
		{
			name:     "on_empty when all files formatted",
			cmd:      "gofmt -l .",
			input:    "\n",
			expected: "gofmt: all files formatted",
		},
		{
			name:     "preserves unformatted file paths",
			cmd:      "gofmt -l .",
			input:    "internal/foo/bar.go\ninternal/baz/qux.go\n",
			expected: "internal/foo/bar.go\ninternal/baz/qux.go",
		},
	}
	runBuiltinCases(t, cases)
}

func TestBuiltinCargo(t *testing.T) {
	cases := []filterCase{
		{
			name: "strips Compiling lines, keeps errors",
			cmd:  "cargo build",
			input: "   Compiling my-crate v0.1.0\n" +
				"   Compiling dep-a v1.2.0\n" +
				"error[E0308]: mismatched types\n" +
				" --> src/main.rs:10:5\n" +
				"error: could not compile `my-crate`\n",
			expected: "error[E0308]: mismatched types\n --> src/main.rs:10:5\nerror: could not compile `my-crate`",
		},
		{
			name: "on_empty after full strip",
			cmd:  "cargo check",
			input: "   Compiling dep v0.1.0\n" +
				"   Fresh dep2 v1.0.0\n" +
				"\n",
			expected: "cargo: ok",
		},
	}
	runBuiltinCases(t, cases)
}

func TestBuiltinTerraformPlan(t *testing.T) {
	cases := []filterCase{
		{
			name: "strips Refreshing state and blank lines",
			cmd:  "terraform plan",
			input: "Acquiring state lock. This may take a few moments...\n" +
				"Refreshing state... [id=vpc-abc]\n" +
				"Refreshing state... [id=sg-123]\n" +
				"Releasing state lock. This may take a few moments...\n" +
				"\n" +
				"Terraform will perform the following actions:\n" +
				"\n" +
				"  # aws_instance.web will be created\n" +
				"  + resource \"aws_instance\" \"web\" {}\n" +
				"\n" +
				"Plan: 1 to add, 0 to change, 0 to destroy.\n",
			expected: "Terraform will perform the following actions:\n  # aws_instance.web will be created\n  + resource \"aws_instance\" \"web\" {}\nPlan: 1 to add, 0 to change, 0 to destroy.",
		},
		{
			name:     "on_empty when no changes",
			cmd:      "terraform plan",
			input:    "Refreshing state... [id=vpc-abc]\n\n",
			expected: "terraform plan: no changes detected",
		},
	}
	runBuiltinCases(t, cases)
}

func TestBuiltinGitStatus(t *testing.T) {
	cases := []filterCase{
		{
			name: "strips branch and tracking noise",
			cmd:  "git status",
			input: "On branch main\n" +
				"Your branch is up to date with 'origin/main'.\n" +
				"\n" +
				"Changes not staged for commit:\n" +
				"  (use \"git add <file>...\" to update what will be committed)\n" +
				"\tmodified:   internal/foo/bar.go\n",
			// (use "git add...") is stripped by the ^\s*\(use "git pattern.
			expected: "Changes not staged for commit:\n\tmodified:   internal/foo/bar.go",
		},
		{
			name: "on_empty for clean working tree",
			cmd:  "git status",
			input: "On branch main\n" +
				"Your branch is up to date with 'origin/main'.\n" +
				"\n" +
				"nothing to commit, working tree clean\n",
			expected: "git status: clean",
		},
	}
	runBuiltinCases(t, cases)
}

// runBuiltinCases runs a slice of filterCase against the shared Engine.
func runBuiltinCases(t *testing.T, cases []filterCase) {
	t.Helper()
	eng := NewEngine()
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			filtered, _, matched := eng.Apply(tc.cmd, tc.input)
			if !matched {
				t.Fatalf("command %q: expected a filter to match, got passthrough", tc.cmd)
			}
			if filtered != tc.expected {
				t.Errorf("command %q:\n  got:  %q\n  want: %q", tc.cmd, filtered, tc.expected)
			}
		})
	}
}
