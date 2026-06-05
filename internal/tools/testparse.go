package tools

import (
	"fmt"
	"regexp"
	"strings"
)

// testFailure is one failed test extracted from a test-runner's output. Name is
// the runner-native identifier (e.g. "TestFoo", "tests/test_x.py::test_y",
// "tests::it_works"); Detail is the first associated assertion/panic line when
// one could be located, otherwise empty.
type testFailure struct {
	Name   string `json:"name"`
	Detail string `json:"detail,omitempty"`
}

// Metadata keys the bash tool sets when a recognized test runner reported
// failures, so downstream consumers (the agent loop, the TUI) can react to
// individual failing tests rather than re-parsing free-form output.
const (
	// MetadataTestFailures holds a []testFailure for the failed tests.
	MetadataTestFailures = "test_failures"
	// MetadataTestFailedCount holds the int count of failed tests.
	MetadataTestFailedCount = "test_failed_count"
)

// parseTestFailures recognizes the command as a go/pytest/jest(npm)/cargo test
// invocation and extracts the failing tests from its output. It returns nil when
// the command is not a known test runner or when no failures are present, so it
// is safe to call unconditionally. The command (not the output) selects the
// parser: guessing a runner from arbitrary output risks false positives on
// ordinary logs that happen to contain words like "FAILED".
func parseTestFailures(command, output string) []testFailure {
	switch classifyTestRunner(command) {
	case runnerGo:
		return parseGoTestFailures(output)
	case runnerPytest:
		return parsePytestFailures(output)
	case runnerJest:
		return parseJestFailures(output)
	case runnerCargo:
		return parseCargoTestFailures(output)
	default:
		return nil
	}
}

type testRunner int

const (
	runnerNone testRunner = iota
	runnerGo
	runnerPytest
	runnerJest
	runnerCargo
)

// Word-boundary matchers for the command-name runners, so "go testing the
// waters" or "cargo testbed" do not misclassify as test invocations. \b after
// "test" requires a following non-word char (space, flag, end) — "go test" and
// "go test ./..." match, "go testing" does not.
var (
	goTestRe    = regexp.MustCompile(`\bgo test\b`)
	cargoTestRe = regexp.MustCompile(`\bcargo test\b`)
)

// classifyTestRunner inspects the command string for a known test-runner
// invocation. Matching is lowercased and tolerant of wrappers (env prefixes,
// &&-chains, flags), but uses word boundaries for the command-name runners to
// avoid matching prose that merely contains "go test".
func classifyTestRunner(command string) testRunner {
	c := strings.ToLower(command)
	switch {
	case cargoTestRe.MatchString(c):
		return runnerCargo
	case goTestRe.MatchString(c):
		return runnerGo
	case strings.Contains(c, "pytest"), strings.Contains(c, "py.test"):
		return runnerPytest
	case strings.Contains(c, "jest"), strings.Contains(c, "vitest"),
		strings.Contains(c, "npm test"), strings.Contains(c, "npm t "),
		strings.Contains(c, "npm run test"), strings.Contains(c, "yarn test"),
		strings.Contains(c, "pnpm test"):
		return runnerJest
	default:
		return runnerNone
	}
}

var (
	// "--- FAIL: TestName (0.00s)" — name is the first token after the colon.
	goFailRe = regexp.MustCompile(`^\s*--- FAIL: (\S+)`)
	// An indented "file_test.go:42: message" detail line under a FAIL marker.
	goDetailRe = regexp.MustCompile(`^\s+(\S+\.go:\d+:.*)$`)
	// A "panic: message" line, emitted (at column 0) when a test panics rather
	// than failing an assertion. A trailing " [recovered]" is the testing
	// framework's marker, not part of the message, so it is dropped.
	goPanicRe = regexp.MustCompile(`^panic: (.*?)(?: \[recovered\])?$`)
	// "FAIL\tgithub.com/x/y [build failed]" — a package that failed to compile
	// (or whose test setup failed) rather than a failing assertion. Go emits no
	// "--- FAIL:" line in this case, so without separate handling a failed run
	// would surface zero structured failures.
	goBuildFailRe = regexp.MustCompile(`^FAIL\s+(\S+) \[(build failed|setup failed)\]$`)
	// A compiler/vet diagnostic at column 0, e.g. "./foo.go:10:2: undefined: x"
	// or an absolute path. Used as the detail for a build failure. Indented
	// assertion details are matched by goDetailRe instead, so the two do not
	// collide.
	goCompileErrRe = regexp.MustCompile(`^\S*\.go:\d+:\d+: .+`)
)

// parseGoTestFailures handles `go test` verbose/non-verbose output. Each
// "--- FAIL:" line names a failed test; the detail is the first following
// indented "file.go:line:" line or "panic:" line (before the next "---" marker),
// so both assertion failures and panics surface a message. A package that fails
// to compile produces a "FAIL pkg [build failed]" entry instead, with the first
// compiler error in that package's block as its detail.
func parseGoTestFailures(output string) []testFailure {
	lines := splitLines(output)
	var failures []testFailure
	// The first compiler error since the current package's "# pkg" header, used
	// as the detail when that package reports a build failure.
	compileErr := ""
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if strings.HasPrefix(line, "# ") {
			// A new package block begins in the build output; any earlier
			// compiler error belonged to a different package.
			compileErr = ""
			continue
		}
		if compileErr == "" && goCompileErrRe.MatchString(line) {
			compileErr = strings.TrimSpace(line)
			continue
		}
		if m := goBuildFailRe.FindStringSubmatch(line); m != nil {
			f := testFailure{Name: m[1] + " [" + m[2] + "]"}
			if compileErr != "" {
				f.Detail = compileErr
			}
			failures = append(failures, f)
			compileErr = ""
			continue
		}
		m := goFailRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		f := testFailure{Name: m[1]}
		for j := i + 1; j < len(lines); j++ {
			if strings.HasPrefix(strings.TrimSpace(lines[j]), "--- ") {
				break
			}
			if d := goDetailRe.FindStringSubmatch(lines[j]); d != nil {
				f.Detail = strings.TrimSpace(d[1])
				break
			}
			if p := goPanicRe.FindStringSubmatch(lines[j]); p != nil {
				f.Detail = "panic: " + strings.TrimSpace(p[1])
				break
			}
		}
		failures = append(failures, f)
	}
	return failures
}

// "FAILED tests/test_x.py::test_y - AssertionError: ..." (pytest short summary).
// "ERROR" lines appear here too, for collection/fixture/teardown errors that
// never reach an assertion; both are actionable failures the agent must see.
var pytestFailRe = regexp.MustCompile(`^(?:FAILED|ERROR) (\S+)(?: - (.*))?$`)

// "tests/test_x.py::test_y FAILED" (pytest verbose, no summary).
var pytestVerboseRe = regexp.MustCompile(`^(\S+::\S+) (?:FAILED|ERROR)\b`)

// parsePytestFailures prefers the short-summary "FAILED/ERROR <id> - <msg>"
// lines and falls back to verbose "<id> FAILED" lines when no summary is present.
func parsePytestFailures(output string) []testFailure {
	lines := splitLines(output)
	var failures []testFailure
	seen := map[string]bool{}
	for _, ln := range lines {
		if m := pytestFailRe.FindStringSubmatch(ln); m != nil {
			if !seen[m[1]] {
				seen[m[1]] = true
				failures = append(failures, testFailure{Name: m[1], Detail: strings.TrimSpace(m[2])})
			}
		}
	}
	if len(failures) > 0 {
		return failures
	}
	for _, ln := range lines {
		if m := pytestVerboseRe.FindStringSubmatch(ln); m != nil {
			if !seen[m[1]] {
				seen[m[1]] = true
				failures = append(failures, testFailure{Name: m[1]})
			}
		}
	}
	return failures
}

var (
	// Jest/vitest mark a failed test with "✕" or "×"; trim a trailing "(N ms)".
	jestFailRe = regexp.MustCompile(`^\s*[✕×] (.+?)(?:\s*\(\d+\s*ms\))?$`)
	jestTimeRe = regexp.MustCompile(`\s*\(\d+\s*ms\)\s*$`)
)

// parseJestFailures collects the "✕ <name>" lines emitted by jest and vitest.
// Detailed assertion blocks ("● <suite> › <name>") are not matched up here;
// reliably pairing them across reporters is brittle, and the failing names are
// the actionable signal.
func parseJestFailures(output string) []testFailure {
	lines := splitLines(output)
	var failures []testFailure
	for _, ln := range lines {
		if m := jestFailRe.FindStringSubmatch(ln); m != nil {
			name := strings.TrimSpace(jestTimeRe.ReplaceAllString(m[1], ""))
			if name != "" {
				failures = append(failures, testFailure{Name: name})
			}
		}
	}
	return failures
}

var (
	// "test tests::it_works ... FAILED" (cargo libtest).
	cargoFailRe = regexp.MustCompile(`^test (\S+) \.\.\. FAILED$`)
	// "thread 'tests::it_works' panicked at ..." carries the test name.
	cargoPanicRe = regexp.MustCompile(`^thread '([^']+)' panicked at (.*)$`)
	// "error: could not compile `crate` (lib test) due to N previous errors" —
	// the terminal line cargo prints when the crate (or its tests) fail to
	// compile. No "... FAILED" lines are emitted in this case, so without
	// separate handling a failed run would surface zero structured failures
	// (mirroring the Go "[build failed]" path).
	cargoCompileFailRe = regexp.MustCompile("^error: could not compile `([^`]+)`(?: \\(([^)]+)\\))?")
	// A rustc diagnostic header at column 0, e.g. "error[E0425]: cannot find
	// value `x` in this scope". Used as the build failure's detail.
	cargoCompileErrRe = regexp.MustCompile(`^(error\[E\d+\]: .+)$`)
)

// parseCargoTestFailures collects "test <name> ... FAILED" lines and attaches
// the matching "thread '<name>' panicked at ..." detail when present. When the
// crate fails to compile, cargo emits no "... FAILED" lines, so a
// "could not compile `crate` ..." marker is surfaced as a "[build failed]"
// entry instead, with the first rustc diagnostic as its detail.
func parseCargoTestFailures(output string) []testFailure {
	lines := splitLines(output)
	panics := map[string]string{}
	compileErr := ""
	for _, ln := range lines {
		if m := cargoPanicRe.FindStringSubmatch(ln); m != nil {
			panics[m[1]] = strings.TrimSpace(m[2])
		}
		if compileErr == "" {
			if m := cargoCompileErrRe.FindStringSubmatch(ln); m != nil {
				compileErr = strings.TrimSpace(m[1])
			}
		}
	}
	var failures []testFailure
	for _, ln := range lines {
		if m := cargoFailRe.FindStringSubmatch(ln); m != nil {
			failures = append(failures, testFailure{Name: m[1], Detail: panics[m[1]]})
			continue
		}
		if m := cargoCompileFailRe.FindStringSubmatch(ln); m != nil {
			name := m[1]
			if m[2] != "" {
				name += " (" + m[2] + ")"
			}
			failures = append(failures, testFailure{Name: name + " [build failed]", Detail: compileErr})
		}
	}
	return failures
}

// summarizeTestFailures renders a compact, agent-friendly block listing the
// failed tests (and their detail line when known). Returns "" for no failures.
func summarizeTestFailures(failures []testFailure) string {
	if len(failures) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[test failures: %d]", len(failures))
	for _, f := range failures {
		if f.Detail != "" {
			fmt.Fprintf(&b, "\n  %s — %s", f.Name, f.Detail)
		} else {
			fmt.Fprintf(&b, "\n  %s", f.Name)
		}
	}
	return b.String()
}
