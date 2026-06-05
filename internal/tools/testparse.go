package tools

import (
	"encoding/json"
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
	case runnerUnittest:
		return parseUnittestFailures(output)
	case runnerJest:
		return parseJestFailures(output)
	case runnerCargo:
		return parseCargoTestFailures(output)
	case runnerRSpec:
		return parseRSpecFailures(output)
	case runnerPHPUnit:
		return parsePHPUnitFailures(output)
	case runnerDotnet:
		return parseDotnetTestFailures(output)
	case runnerMaven:
		return parseMavenTestFailures(output)
	case runnerGradle:
		return parseGradleTestFailures(output)
	case runnerExUnit:
		return parseExUnitFailures(output)
	case runnerTAP:
		return parseTAPFailures(output)
	case runnerDeno:
		return parseDenoTestFailures(output)
	case runnerSwift:
		return parseSwiftTestFailures(output)
	case runnerBun:
		return parseBunTestFailures(output)
	case runnerMocha:
		return parseMochaFailures(output)
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
	runnerRSpec
	runnerUnittest
	runnerPHPUnit
	runnerDotnet
	runnerMaven
	runnerGradle
	runnerExUnit
	runnerTAP
	runnerDeno
	runnerSwift
	runnerBun
	runnerMocha
)

// Word-boundary matchers for the command-name runners, so "go testing the
// waters" or "cargo testbed" do not misclassify as test invocations. \b after
// "test" requires a following non-word char (space, flag, end) — "go test" and
// "go test ./..." match, "go testing" does not.
var (
	goTestRe    = regexp.MustCompile(`\bgo test\b`)
	cargoTestRe = regexp.MustCompile(`\bcargo test\b`)
	// \brspec\b matches "rspec", "bundle exec rspec", and "bin/rspec" (the slash
	// is a word boundary) without firing on prose like "rspecs are great".
	rspecRe = regexp.MustCompile(`\brspec\b`)
	// "mvn", "mvnw", and "./mvnw" all begin with "mvn" at a word boundary; the
	// \b keeps prose like "an mvndaemon discussion" from matching while allowing
	// the wrapper-script suffix.
	mavenRe = regexp.MustCompile(`\bmvn`)
	// "gradle", "gradlew", and "./gradlew" all begin with "gradle" at a word
	// boundary; the optional "w" admits the wrapper script. \b keeps prose like
	// "an upgrade plan" from matching while allowing the wrapper-script suffix.
	gradleRe = regexp.MustCompile(`\bgradlew?\b`)
	// "node --test"/"node:test" drive Node's built-in test runner, whose non-TTY
	// (CI) default reporter emits TAP; "tape" is the classic standalone
	// TAP-emitting runner. All three produce "not ok N - <desc>" failure lines, so
	// a single TAP parser covers them. \btape\b keeps prose like "tapestry" from
	// matching while admitting "node tape.js" / "npx tape".
	tapRe = regexp.MustCompile(`node\s+--test\b|node:test\b|\btape\b`)
	// "bats" (Bash Automated Testing System) emits TAP by default ("not ok N
	// <desc>" with the assertion on following "# ..." comment lines), so it shares
	// the TAP parser. \bbats\b admits "bats", "bats test.bats", and "npx bats"
	// while keeping prose like "acrobats perform" from matching.
	batsRe = regexp.MustCompile(`\bbats\b`)
	// "swift test" drives SwiftPM's XCTest runner; \b after "test" keeps prose
	// like "swift testing guide" from matching while admitting flags and paths
	// ("swift test --filter FooTests").
	swiftTestRe = regexp.MustCompile(`\bswift test\b`)
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
	case rspecRe.MatchString(c):
		return runnerRSpec
	case mavenRe.MatchString(c):
		// `mvn test`/`mvn verify` (and the `mvnw` wrapper) drive the Surefire
		// plugin, whose console output marks each failure with "<<< FAILURE!" or
		// "<<< ERROR!" regardless of the JUnit/TestNG version underneath.
		return runnerMaven
	case gradleRe.MatchString(c):
		// `gradle test`/`gradle check`/`gradle build` (and the `gradlew` wrapper)
		// drive Gradle's test task, whose console output marks each failure with a
		// "<Class> > <test> FAILED" header regardless of the JUnit/TestNG/Spock
		// framework underneath.
		return runnerGradle
	case strings.Contains(c, "mix test"):
		// `mix test` drives Elixir's ExUnit, whose failure report numbers each
		// failing test ("  1) test <name> (<Module>)") and prints the source
		// location and assertion/exception message on the indented lines beneath.
		return runnerExUnit
	case strings.Contains(c, "dotnet test"):
		// `dotnet test` drives VSTest, whose console logger prints "Failed
		// <FQN> [<time>]" per failure regardless of the underlying framework
		// (xUnit/NUnit/MSTest), so one parser covers all three.
		return runnerDotnet
	case strings.Contains(c, "phpunit"):
		// "phpunit", "vendor/bin/phpunit", "php artisan test" wrappers all carry
		// the binary name; matching it before the JS/Python runners avoids any
		// overlap (none of those substrings appear in a phpunit invocation).
		return runnerPHPUnit
	case strings.Contains(c, "pytest"), strings.Contains(c, "py.test"):
		return runnerPytest
	// `python -m unittest` (and `unittest discover`) print a "FAIL:"/"ERROR:"
	// summary distinct from pytest's, so they get their own parser.
	case strings.Contains(c, "unittest"):
		return runnerUnittest
	case tapRe.MatchString(c), batsRe.MatchString(c):
		return runnerTAP
	case strings.Contains(c, "deno test"):
		// `deno test` runs Deno's built-in test runner, whose console output marks
		// each failure with a "<name> ... FAILED" line and repeats it as
		// "<name> => <location>" in the trailing "FAILURES" block. Checked before
		// the JS runners since a "deno test" invocation carries none of their
		// substrings, but keeping it explicit guards against future overlap.
		return runnerDeno
	case swiftTestRe.MatchString(c):
		// `swift test` runs SwiftPM's XCTest runner, whose output marks each
		// failure with a "Test Case '<id>' failed" line and prints the assertion
		// on a "<file>.swift:<line>: error: <id> : <message>" line. Checked before
		// the JS runners since a "swift test" invocation carries none of their
		// substrings, but kept explicit to guard against future overlap.
		return runnerSwift
	case strings.Contains(c, "bun test"):
		// `bun test` runs Bun's built-in test runner, whose default reporter marks
		// each failing test with a "✗" glyph rather than the "✕"/"×" jest uses, so
		// it needs its own parser. Checked before the JS runners since a "bun test"
		// invocation carries none of their substrings, but kept explicit to guard
		// against future overlap. `bun run test` (an arbitrary npm script) has "run"
		// between the words and so does not match here.
		return runnerBun
	case strings.Contains(c, "mocha"):
		// `mocha`/`npx mocha` drives Mocha's reporters, whose default/spec output
		// closes with an "N failing" block that numbers each failure ("  1) <title>")
		// and prints the assertion/exception beneath — a shape none of the other JS
		// runners share. Checked before the jest runners since a "mocha" invocation
		// carries none of their substrings, but kept explicit to guard against
		// future overlap.
		return runnerMocha
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
	// `go test -json` (and wrappers like gotestsum) emit a newline-delimited
	// event stream rather than the "--- FAIL:" lines the text parser keys on.
	// Detect and dispatch to the JSON parser so CI/IDE-style invocations still
	// surface structured failures.
	if looksLikeGoTestJSON(output) {
		return parseGoTestJSONFailures(output)
	}

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

// goJSONEvent is one event from the `go test -json` stream (the shape produced
// by the testing package's JSON output). Only the fields the parser needs are
// decoded; unknown fields are ignored.
type goJSONEvent struct {
	Action  string `json:"Action"`
	Test    string `json:"Test"`
	Package string `json:"Package"`
	Output  string `json:"Output"`
}

// looksLikeGoTestJSON reports whether output is a `go test -json` event stream
// (newline-delimited JSON objects carrying an "Action" field) rather than the
// human-readable verbose output. Only the first non-blank line is inspected: a
// genuine stream begins with an event, and prose that merely contains JSON
// later does not warrant the JSON parser.
func looksLikeGoTestJSON(output string) bool {
	for _, ln := range splitLines(output) {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		if !strings.HasPrefix(ln, "{") {
			return false
		}
		var ev goJSONEvent
		if err := json.Unmarshal([]byte(ln), &ev); err != nil {
			return false
		}
		return ev.Action != ""
	}
	return false
}

// parseGoTestJSONFailures extracts failing tests from a `go test -json` stream.
// A test fails when it receives an "Action":"fail" event carrying a Test name.
// The detail is the first assertion ("file.go:line:") or panic line seen in that
// test's "output" events, mirroring the text parser. Failing tests are returned
// in first-seen order for deterministic output.
//
// A package-level "fail" event (no Test) usually accompanies its individual test
// failures and is ignored — but when the package failed without any test failing
// and its output carried a compiler diagnostic, it is a build failure. Those are
// surfaced as a "pkg [build failed]" entry after the test failures, matching the
// text parser's handling of "FAIL pkg [build failed]" (which `-json` never emits).
func parseGoTestJSONFailures(output string) []testFailure {
	var order []string
	failed := map[string]bool{}
	detail := map[string]string{}
	// Per-package state for surfacing build failures: the count of failed tests
	// (to suppress the package entry once individual tests reported), the first
	// compiler diagnostic seen, and the order packages first failed in.
	testsInPkg := map[string]int{}
	compileErrByPkg := map[string]string{}
	pkgFailed := map[string]bool{}
	var pkgOrder []string
	for _, ln := range splitLines(output) {
		ln = strings.TrimSpace(ln)
		if !strings.HasPrefix(ln, "{") {
			continue
		}
		var ev goJSONEvent
		if err := json.Unmarshal([]byte(ln), &ev); err != nil {
			continue
		}
		switch ev.Action {
		case "output":
			if ev.Test == "" {
				// Package-scoped output: capture the first compiler diagnostic in
				// case this package turns out to be a build failure.
				if ev.Package != "" {
					if _, ok := compileErrByPkg[ev.Package]; !ok {
						if d := goCompileErrFromOutput(ev.Output); d != "" {
							compileErrByPkg[ev.Package] = d
						}
					}
				}
				continue
			}
			if _, ok := detail[ev.Test]; ok {
				continue // keep the first detail line per test
			}
			if d := goDetailFromOutput(ev.Output); d != "" {
				detail[ev.Test] = d
			}
		case "fail":
			if ev.Test == "" {
				if ev.Package != "" && !pkgFailed[ev.Package] {
					pkgFailed[ev.Package] = true
					pkgOrder = append(pkgOrder, ev.Package)
				}
				continue
			}
			if ev.Package != "" {
				testsInPkg[ev.Package]++
			}
			if !failed[ev.Test] {
				failed[ev.Test] = true
				order = append(order, ev.Test)
			}
		}
	}
	var failures []testFailure
	for _, name := range order {
		failures = append(failures, testFailure{Name: name, Detail: detail[name]})
	}
	// Append build failures for packages that failed without any individual test
	// failing and whose output carried a compiler diagnostic.
	for _, pkg := range pkgOrder {
		if testsInPkg[pkg] > 0 {
			continue
		}
		ce := compileErrByPkg[pkg]
		if ce == "" {
			continue // failed for some other reason already reported via its tests
		}
		failures = append(failures, testFailure{Name: pkg + " [build failed]", Detail: ce})
	}
	return failures
}

// goDetailFromOutput pulls an assertion or panic message out of a single
// "output" event's text, reusing the same matchers as the text parser so JSON
// and verbose output yield identical detail lines. Returns "" when the line is
// neither.
func goDetailFromOutput(out string) string {
	line := strings.TrimRight(out, "\r\n")
	if d := goDetailRe.FindStringSubmatch(line); d != nil {
		return strings.TrimSpace(d[1])
	}
	if p := goPanicRe.FindStringSubmatch(strings.TrimSpace(line)); p != nil {
		return "panic: " + strings.TrimSpace(p[1])
	}
	return ""
}

// goCompileErrFromOutput pulls a compiler diagnostic ("./foo.go:10:2: ...") out
// of a single package-scoped "output" event, reusing the text parser's matcher
// so JSON and verbose build failures yield identical detail. Returns "" when the
// line is not a compiler diagnostic (e.g. the "# pkg" header or a FAIL line).
func goCompileErrFromOutput(out string) string {
	line := strings.TrimRight(out, "\r\n")
	if goCompileErrRe.MatchString(line) {
		return strings.TrimSpace(line)
	}
	return ""
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
	// "FAIL: test_upper (test_module.TestStringMethods)" — a failed assertion;
	// "ERROR: ..." marks an uncaught exception in setup/teardown/the test body.
	// The captured group is the test id "method (module.Class)", which is exactly
	// what `python -m unittest module.Class.method` re-runs. A trailing
	// " (subTest ...)" descriptor (from assertSubTest) is kept as part of the name.
	unittestFailRe = regexp.MustCompile(`^(?:FAIL|ERROR): (\S+ \(.+\))$`)
	// The unindented exception line that closes a unittest traceback, e.g.
	// "AssertionError: 'FOO' != 'FOOO'". Only the standard Error/Exception/Warning
	// suffixes are matched so indented traceback frames ("  File ...", code lines)
	// and the "Traceback (most recent call last):" header are skipped.
	unittestDetailRe = regexp.MustCompile(`^([A-Za-z_][\w.]*(?:Error|Exception|Warning)(?::.*)?)$`)
)

// parseUnittestFailures handles Python's stdlib `unittest` output. Each failure
// block opens with a "FAIL: <id>" or "ERROR: <id>" header; the detail is the
// terminating exception line of that block's traceback (e.g. "AssertionError:
// ..."), located before the next "====" separator or the next FAIL/ERROR header.
func parseUnittestFailures(output string) []testFailure {
	lines := splitLines(output)
	var failures []testFailure
	seen := map[string]bool{}
	for i := 0; i < len(lines); i++ {
		m := unittestFailRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		name := strings.TrimSpace(m[1])
		if seen[name] {
			continue
		}
		seen[name] = true
		f := testFailure{Name: name}
		for j := i + 1; j < len(lines); j++ {
			if strings.HasPrefix(lines[j], "====") || unittestFailRe.MatchString(lines[j]) {
				break
			}
			if d := unittestDetailRe.FindStringSubmatch(lines[j]); d != nil {
				f.Detail = strings.TrimSpace(d[1])
				break
			}
		}
		failures = append(failures, f)
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

var (
	// "rspec ./spec/foo_spec.rb:10 # MyClass#method does something" — a line from
	// RSpec's "Failed examples:" summary, printed by the default formatter
	// regardless of the progress/documentation style. The path:line is the
	// re-runnable id; the text after " # " is the example's description.
	rspecFailedExampleRe = regexp.MustCompile(`^rspec (\S+) # (.+)$`)
	// "  1) MyClass#method does something" — the numbered header of an entry in
	// the "Failures:" block, used to attach the assertion message as a detail.
	rspecFailureHeaderRe = regexp.MustCompile(`^\s*\d+\) (.+)$`)
	// "     Failure/Error: expect(x).to eq(y)" — the first line of a failure's
	// body, the closest thing RSpec prints to a one-line assertion message.
	rspecFailureErrorRe = regexp.MustCompile(`^\s*Failure/Error: (.+)$`)
)

// parseRSpecFailures extracts failures from RSpec output. The "Failed examples:"
// summary gives a clean, single-line "rspec <location> # <description>" per
// failure (Name is the description, Detail the re-runnable location). When that
// summary is absent — e.g. a suite that errored before printing it — it falls
// back to the numbered "Failures:" block, pairing each "N) <description>" header
// with its following "Failure/Error:" line as the detail.
func parseRSpecFailures(output string) []testFailure {
	lines := splitLines(output)
	var failures []testFailure
	seen := map[string]bool{}
	for _, ln := range lines {
		if m := rspecFailedExampleRe.FindStringSubmatch(ln); m != nil {
			desc := strings.TrimSpace(m[2])
			if !seen[desc] {
				seen[desc] = true
				failures = append(failures, testFailure{Name: desc, Detail: strings.TrimSpace(m[1])})
			}
		}
	}
	if len(failures) > 0 {
		return failures
	}
	for i := 0; i < len(lines); i++ {
		m := rspecFailureHeaderRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		f := testFailure{Name: strings.TrimSpace(m[1])}
		for j := i + 1; j < len(lines) && j <= i+6; j++ {
			if e := rspecFailureErrorRe.FindStringSubmatch(lines[j]); e != nil {
				f.Detail = strings.TrimSpace(e[1])
				break
			}
		}
		if !seen[f.Name] {
			seen[f.Name] = true
			failures = append(failures, f)
		}
	}
	return failures
}

// "1) App\Tests\MyTest::testSomething" — the numbered header of an entry in
// PHPUnit's "There was 1 failure:"/"There were N errors:" report. PHPUnit
// numbers the Failures and Errors blocks separately, each restarting at 1, so
// the test id (not the number) keys deduplication. The id is "Class::method",
// optionally followed by a data-set descriptor ("with data set #0 (...)").
var phpunitHeaderRe = regexp.MustCompile(`^\d+\) (\S+::\S+.*)$`)

// parsePHPUnitFailures extracts failing tests from PHPUnit's text report. Each
// failure or error block opens with a "N) Class::method" header; the detail is
// the first non-empty line beneath it, which carries the assertion message
// ("Failed asserting that ...") or the thrown exception ("RuntimeException:
// boom"). The trailing "/path/File.php:line" location and surrounding blanks are
// skipped. A test appearing in both blocks (impossible in practice, but cheap to
// guard) is reported once.
func parsePHPUnitFailures(output string) []testFailure {
	lines := splitLines(output)
	var failures []testFailure
	seen := map[string]bool{}
	for i := 0; i < len(lines); i++ {
		m := phpunitHeaderRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		name := strings.TrimSpace(m[1])
		if seen[name] {
			continue
		}
		seen[name] = true
		f := testFailure{Name: name}
		// The message sits on the first non-empty line after the header. Stop at
		// the next numbered header so a block whose entry has no message body does
		// not borrow the following entry's message.
		for j := i + 1; j < len(lines); j++ {
			if phpunitHeaderRe.MatchString(lines[j]) {
				break
			}
			if t := strings.TrimSpace(lines[j]); t != "" {
				f.Detail = t
				break
			}
		}
		failures = append(failures, f)
	}
	return failures
}

var (
	// "  Failed MyApp.Tests.CalcTests.Add [4 ms]" — the per-test failure line
	// the VSTest console logger prints for `dotnet test`. The captured group is
	// everything after "Failed " up to the trailing "[<time>]" duration marker,
	// which dotnetTimeRe strips. The fully-qualified name (optionally with a
	// "(arg: ...)" data-row suffix) is the re-runnable test id. Only the literal
	// "Failed" prefix is matched so ordinary prose is not misclassified.
	dotnetFailRe = regexp.MustCompile(`^\s*Failed\s+(\S.*?)\s*$`)
	// The trailing " [4 ms]" / " [< 1 ms]" / " [1 s]" duration marker on a
	// failure line; matched non-greedily off the end so a bracketed data-row
	// argument earlier in the name is preserved.
	dotnetTimeRe = regexp.MustCompile(`\s*\[[^\]]*\]\s*$`)
)

// parseDotnetTestFailures extracts failing tests from `dotnet test` (VSTest)
// console output. Each failure opens with a "Failed <FQN> [<time>]" line; the
// detail is the first non-empty line beneath the "Error Message:" header that
// follows it (the assertion or thrown-exception message), located before the
// next "Failed" line so an entry without a message does not borrow the next
// one's. The "Failed!  - Failed: N, ..." run summary is not matched: its "!"
// breaks the required whitespace after "Failed".
func parseDotnetTestFailures(output string) []testFailure {
	lines := splitLines(output)
	var failures []testFailure
	seen := map[string]bool{}
	for i := 0; i < len(lines); i++ {
		m := dotnetFailRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		name := strings.TrimSpace(dotnetTimeRe.ReplaceAllString(m[1], ""))
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		f := testFailure{Name: name}
		for j := i + 1; j < len(lines); j++ {
			if dotnetFailRe.MatchString(lines[j]) {
				break
			}
			if strings.TrimSpace(lines[j]) != "Error Message:" {
				continue
			}
			for k := j + 1; k < len(lines); k++ {
				if t := strings.TrimSpace(lines[k]); t != "" {
					f.Detail = t
					break
				}
			}
			break
		}
		failures = append(failures, f)
	}
	return failures
}

var (
	// "<<< FAILURE!" / "<<< ERROR!" — Surefire's per-test outcome marker. It
	// appears both on the per-test header line and on the "Tests run: ..." run
	// summary line, so the parser excludes the latter explicitly.
	mavenFailMarkerRe = regexp.MustCompile(`<<<\s+(?:FAILURE|ERROR)!`)
	// The fully-qualified test id sits before the "  Time elapsed: ..." segment of
	// the failure header, after an optional Maven "[ERROR]"/"[INFO]"/"[WARNING]"
	// log prefix. Surefire prints it as "com.example.FooTest.bar" (JUnit5) or
	// "bar(com.example.FooTest)" (JUnit4); both are kept verbatim as the name.
	mavenNameRe = regexp.MustCompile(`^(?:\[(?:ERROR|INFO|WARNING)\]\s+)?(\S.*?)\s+Time elapsed:`)
)

// parseMavenTestFailures extracts failing tests from Maven Surefire console
// output (`mvn test`/`mvn verify`). Each failure opens with a
// "<FQN>  Time elapsed: <t> s  <<< FAILURE!" (or "<<< ERROR!") header; the
// detail is the exception line printed immediately beneath it (e.g.
// "org.opentest4j.AssertionFailedError: expected: <5> but was: <4>"), located as
// the first unindented non-empty line before the next failure header so stack
// frames (indented "at ...") and the next entry are skipped. The aggregate
// "Tests run: N, Failures: ... <<< FAILURE!" line carries the same marker but is
// not a test, so it is excluded.
func parseMavenTestFailures(output string) []testFailure {
	lines := splitLines(output)
	var failures []testFailure
	seen := map[string]bool{}
	for i := 0; i < len(lines); i++ {
		if !mavenFailMarkerRe.MatchString(lines[i]) {
			continue
		}
		m := mavenNameRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		name := strings.TrimSpace(m[1])
		// The run-summary line ("Tests run: 2, Failures: 1, ... <<< FAILURE!")
		// matches the marker and name shapes but is a count, not a test.
		if name == "" || strings.HasPrefix(name, "Tests run:") || seen[name] {
			continue
		}
		seen[name] = true
		f := testFailure{Name: name}
		for j := i + 1; j < len(lines); j++ {
			if mavenFailMarkerRe.MatchString(lines[j]) {
				break
			}
			// The exception message is at column 0; indented lines are stack
			// frames ("\tat ...") and blank lines are separators.
			if strings.TrimSpace(lines[j]) == "" || lines[j][0] == ' ' || lines[j][0] == '\t' {
				continue
			}
			f.Detail = strings.TrimSpace(lines[j])
			break
		}
		failures = append(failures, f)
	}
	return failures
}

var (
	// "CalculatorTest > testAdd() FAILED" — Gradle's per-test failure header. The
	// display name uses " > " to separate the (possibly fully-qualified) class
	// from the test name, and nested suites add further " > " segments; the whole
	// "Class > test" id is kept verbatim, since `gradle test --tests "<id>"`
	// re-runs it. Gradle's own "> Task :test FAILED" progress line has no " > "
	// separator, so it does not match.
	gradleFailRe = regexp.MustCompile(`^(.+ > .+) FAILED$`)
	// The exception message Gradle indents beneath a failure header, e.g.
	// "    org.opentest4j.AssertionFailedError: expected: <5> but was: <4>". Stack
	// frames are indented further and begin with "at ", so they are skipped.
	gradleDetailRe = regexp.MustCompile(`^\s+(\S.*)$`)
)

// parseGradleTestFailures extracts failing tests from Gradle test console output
// (`gradle test`/`./gradlew test`). Each failure opens with a
// "<Class> > <test> FAILED" header; the detail is the first indented line
// beneath it that is not a stack frame (those begin with "at "), which carries
// the assertion or thrown-exception message. Scanning stops at the next failure
// header so an entry without a message does not borrow the following one's.
func parseGradleTestFailures(output string) []testFailure {
	lines := splitLines(output)
	var failures []testFailure
	seen := map[string]bool{}
	for i := 0; i < len(lines); i++ {
		m := gradleFailRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		name := strings.TrimSpace(m[1])
		if seen[name] {
			continue
		}
		seen[name] = true
		f := testFailure{Name: name}
		for j := i + 1; j < len(lines); j++ {
			if gradleFailRe.MatchString(lines[j]) {
				break
			}
			d := gradleDetailRe.FindStringSubmatch(lines[j])
			if d == nil {
				continue
			}
			text := strings.TrimSpace(d[1])
			if strings.HasPrefix(text, "at ") {
				continue // a stack frame, not the message
			}
			f.Detail = text
			break
		}
		failures = append(failures, f)
	}
	return failures
}

var (
	// "  1) test adds two numbers (CalculatorTest)" — the numbered header of an
	// entry in ExUnit's failure report (`mix test`). ExUnit numbers failures
	// sequentially; the captured group is the full "test <name> (<Module>)"
	// descriptor (or "doctest ..."/"property ..." variant), which identifies the
	// failing test. `describe` blocks fold their label into <name>.
	exunitHeaderRe = regexp.MustCompile(`^\s*\d+\) (.+)$`)
	// "     test/calculator_test.exs:8" — the source location ExUnit prints on the
	// first indented line beneath a failure header. It is skipped when looking for
	// the assertion message, and serves as the fallback detail when no message
	// line follows. Both ".ex" and ".exs" sources appear in stacktraces.
	exunitLocationRe = regexp.MustCompile(`^\s+\S+\.exs?:\d+$`)
)

// parseExUnitFailures extracts failing tests from Elixir ExUnit output
// (`mix test`). Each failure opens with a "  N) test <name> (<Module>)" header;
// the detail is the first indented, non-location message line beneath it — the
// assertion summary ("Assertion with == failed") or raised exception
// ("** (RuntimeError) boom") — located before the next failure header so an
// entry without a message does not borrow the following one's. The source
// location line is used as the detail only when no message line is present.
func parseExUnitFailures(output string) []testFailure {
	lines := splitLines(output)
	var failures []testFailure
	seen := map[string]bool{}
	for i := 0; i < len(lines); i++ {
		m := exunitHeaderRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		name := strings.TrimSpace(m[1])
		if seen[name] {
			continue
		}
		seen[name] = true
		f := testFailure{Name: name}
		location := ""
		for j := i + 1; j < len(lines); j++ {
			if exunitHeaderRe.MatchString(lines[j]) {
				break
			}
			if strings.TrimSpace(lines[j]) == "" {
				continue
			}
			// The failure body is indented under the header; an unindented line
			// ends the block (e.g. the run summary or the next section).
			if lines[j][0] != ' ' && lines[j][0] != '\t' {
				break
			}
			if exunitLocationRe.MatchString(lines[j]) {
				if location == "" {
					location = strings.TrimSpace(lines[j])
				}
				continue
			}
			f.Detail = strings.TrimSpace(lines[j])
			break
		}
		if f.Detail == "" {
			f.Detail = location
		}
		failures = append(failures, f)
	}
	return failures
}

var (
	// "not ok 1 - description" — a failing TAP assertion. The number and the
	// " - " separator are both optional in the spec, and the trailing text is the
	// test description, which may carry a " # TODO"/" # SKIP" directive. The
	// leading number is captured so a directive-only or description-less failure
	// can still be named ("TAP test 1").
	tapNotOkRe = regexp.MustCompile(`^not ok\b\s*(\d+)?\s*-?\s*(.*)$`)
	// A " # TODO ..."/" # SKIP ..." directive suffix on a TAP result line. A
	// "not ok" carrying either directive is an expected/ignored result, not a
	// genuine failure, so it is excluded from the report.
	tapDirectiveRe = regexp.MustCompile(`(?i)\s+#\s*(?:todo|skip)\b`)
	// "  message: expected 1 to equal 2" — the message field of the indented YAML
	// diagnostic block TAP emitters (node:test, tape, node-tap) print beneath a
	// failing assertion. Surrounding quotes on the value are stripped by the
	// caller so single- and double-quoted YAML scalars render the same.
	tapMessageRe = regexp.MustCompile(`^\s+message:\s*(.*\S)\s*$`)
	// "#   `[ "$x" -eq 4 ]' failed" — a TAP diagnostic comment line beneath a
	// failing assertion. bats (and other shell-style emitters) report the failed
	// expression and its location this way rather than in a YAML block, so the
	// first such comment is used as the detail when no "message:" field is found.
	tapCommentRe = regexp.MustCompile(`^\s*#\s?(.*\S)\s*$`)
)

// parseTAPFailures extracts failing tests from TAP (Test Anything Protocol)
// output, as produced by `node --test` in CI, `tape`, `bats`, and other TAP
// emitters. Each "not ok N - <description>" line is a failure; the detail is the
// "message:" field of the indented YAML diagnostic block beneath it, or — when no
// such field is present — the first "# ..." diagnostic comment (the form bats
// uses to report the failed expression), located before the next "ok"/"not ok"
// result or the closing "..." of the block. A "not ok" carrying a "# TODO"/"#
// SKIP" directive is an expected result, not a failure, so it is skipped.
func parseTAPFailures(output string) []testFailure {
	lines := splitLines(output)
	var failures []testFailure
	for i := 0; i < len(lines); i++ {
		m := tapNotOkRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		desc := strings.TrimSpace(m[2])
		if loc := tapDirectiveRe.FindStringIndex(desc); loc != nil {
			// A TODO/SKIP directive marks an expected or ignored result; drop it.
			continue
		}
		name := desc
		if name == "" {
			if m[1] != "" {
				name = "TAP test " + m[1]
			} else {
				name = "TAP test"
			}
		}
		f := testFailure{Name: name}
		// A "#"-comment diagnostic (bats' assertion/location report) is the
		// fallback detail when no YAML "message:" field is found in the block.
		comment := ""
		for j := i + 1; j < len(lines); j++ {
			// The YAML block ends at "..." and never spans past the next result.
			if strings.TrimSpace(lines[j]) == "..." || tapNotOkRe.MatchString(lines[j]) || tapOkRe.MatchString(lines[j]) {
				break
			}
			if d := tapMessageRe.FindStringSubmatch(lines[j]); d != nil {
				f.Detail = strings.Trim(strings.TrimSpace(d[1]), `'"`)
				break
			}
			if comment == "" {
				if d := tapCommentRe.FindStringSubmatch(lines[j]); d != nil {
					comment = strings.TrimSpace(d[1])
				}
			}
		}
		if f.Detail == "" {
			f.Detail = comment
		}
		failures = append(failures, f)
	}
	return failures
}

// "ok 1 - description" — a passing TAP assertion, used only to bound a failure's
// YAML diagnostic scan so it does not bleed into the next result.
var tapOkRe = regexp.MustCompile(`^ok\b`)

var (
	// "subtract ... FAILED (2ms)" — Deno's per-test outcome line. The test name
	// (which may contain spaces, since Deno.test takes an arbitrary string) sits
	// before " ... FAILED"; the trailing " (<time>)" duration marker is optional.
	// Steps print the same shape indented, and a "<name> ... FAILED (N steps)"
	// roll-up can appear for a parent test, but matching the unindented top-level
	// lines is enough to name every failing test.
	denoFailRe = regexp.MustCompile(`^(.+?) \.\.\. FAILED(?: \(.*\))?$`)
	// "subtract => ./math_test.ts:6:6" — a failure entry header in the trailing
	// "ERRORS"/"FAILURES" blocks, pairing a test name with its source location.
	// Used to attach the assertion/exception message that follows as the detail.
	denoErrorHeaderRe = regexp.MustCompile(`^(.+?) => (\S+:\d+:\d+)$`)
	// "error: AssertionError: Values are not equal." — the message line Deno
	// prints beneath an ERRORS-block header. The text after "error: " is kept as
	// the detail.
	denoErrorMsgRe = regexp.MustCompile(`^error: (.*\S)\s*$`)
)

// parseDenoTestFailures extracts failing tests from `deno test` output. Each
// "<name> ... FAILED" line names a failed test, in run order. The detail is the
// "error: <message>" line that follows the test's "<name> => <location>" header
// in the trailing ERRORS block, when present, so assertion and thrown-exception
// messages surface alongside the names.
func parseDenoTestFailures(output string) []testFailure {
	lines := splitLines(output)

	// First pass: map each test name to the first error message in its ERRORS-block
	// entry, so the detail can be attached when the "... FAILED" line is emitted.
	details := map[string]string{}
	for i := 0; i < len(lines); i++ {
		m := denoErrorHeaderRe.FindStringSubmatch(strings.TrimSpace(lines[i]))
		if m == nil {
			continue
		}
		name := strings.TrimSpace(m[1])
		if _, ok := details[name]; ok {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			if d := denoErrorMsgRe.FindStringSubmatch(strings.TrimSpace(lines[j])); d != nil {
				details[name] = strings.TrimSpace(d[1])
				break
			}
			// Stop at the next entry header so an entry without a message does not
			// borrow the following one's.
			if denoErrorHeaderRe.MatchString(strings.TrimSpace(lines[j])) {
				break
			}
		}
	}

	var failures []testFailure
	seen := map[string]bool{}
	for _, ln := range lines {
		// Only top-level (unindented) outcome lines are tests; indented lines are
		// steps whose failure is already reflected in their parent's roll-up.
		if ln != strings.TrimLeft(ln, " \t") {
			continue
		}
		m := denoFailRe.FindStringSubmatch(ln)
		if m == nil {
			continue
		}
		name := strings.TrimSpace(m[1])
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		failures = append(failures, testFailure{Name: name, Detail: details[name]})
	}
	return failures
}

var (
	// "Test Case '-[ModuleTests.MyTests testFoo]' failed (0.001 seconds)." (macOS)
	// or "Test Case 'MyTests.testFoo' failed (0.001 seconds)." (Linux SwiftPM).
	// The quoted text is the failing test's id and is identical to the id printed
	// on the matching "error:" line, so it keys the detail lookup. The "started"
	// and "passed" variants are not matched.
	swiftFailRe = regexp.MustCompile(`^Test Case '(.+)' failed\b`)
	// "/path/Tests/MyTests.swift:14: error: <id> : XCTAssertEqual failed: ..." —
	// the assertion line XCTest prints for a failure, carrying the test id (the
	// same one quoted in the "Test Case" line) and the one-line message.
	swiftErrorRe = regexp.MustCompile(`\.swift:\d+: error: (.+?) : (.+)$`)
)

// parseSwiftTestFailures extracts failing tests from `swift test` (XCTest)
// output. Each "Test Case '<id>' failed" line names a failed test, in run order.
// The detail is the message from the first "<file>.swift:<line>: error: <id> :
// <message>" assertion line carrying the same id, so assertion messages surface
// alongside the names. The id format differs between macOS ("-[Class method]")
// and Linux ("Class.method"), but is consistent within a run, so it pairs the
// two lines regardless of platform.
func parseSwiftTestFailures(output string) []testFailure {
	lines := splitLines(output)

	// First pass: map each test id to the first assertion message printed for it,
	// so the detail can be attached when the "failed" line is seen.
	details := map[string]string{}
	for _, ln := range lines {
		if m := swiftErrorRe.FindStringSubmatch(ln); m != nil {
			id := strings.TrimSpace(m[1])
			if _, ok := details[id]; !ok {
				details[id] = strings.TrimSpace(m[2])
			}
		}
	}

	var failures []testFailure
	seen := map[string]bool{}
	for _, ln := range lines {
		m := swiftFailRe.FindStringSubmatch(ln)
		if m == nil {
			continue
		}
		name := strings.TrimSpace(m[1])
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		failures = append(failures, testFailure{Name: name, Detail: details[name]})
	}
	return failures
}

// "✗ adds two numbers [0.42ms]" — Bun's built-in test runner marks a failed
// test with a "✗" (U+2717) glyph; some terminals/versions render it as "✘"
// (U+2718), so both are accepted. Nested describe blocks are folded into the
// name with " > ". The trailing " [<time>]" duration marker (e.g. "[0.42ms]" or
// "[1.20s]") is optional and stripped.
var bunFailRe = regexp.MustCompile(`^\s*[✗✘]\s+(.+?)(?:\s*\[[\d.]+\s*m?s\])?$`)

// parseBunTestFailures collects the "✗ <name>" lines emitted by `bun test`. Like
// the jest parser it reports the failing names rather than attempting to pair the
// trailing "error:" assertion blocks, which Bun interleaves with source snippets
// in a reporter-dependent way; the names are the actionable signal.
func parseBunTestFailures(output string) []testFailure {
	lines := splitLines(output)
	var failures []testFailure
	seen := map[string]bool{}
	for _, ln := range lines {
		m := bunFailRe.FindStringSubmatch(ln)
		if m == nil {
			continue
		}
		name := strings.TrimSpace(m[1])
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		failures = append(failures, testFailure{Name: name})
	}
	return failures
}

// "  1) Array\n       #indexOf()\n         returns -1:" — the numbered header
// that opens each entry in Mocha's "N failing" block. Mocha prints the full test
// title across one or more increasingly-indented lines, the last ending in a
// colon, before the assertion/exception message. The captured group is the
// header line's text — the outermost title segment, or the whole title (ending
// in ":") when the test has no surrounding describe block.
var mochaHeaderRe = regexp.MustCompile(`^\s*\d+\) (.+)$`)

// "  2 failing" — the run-summary line Mocha's reporters print immediately before
// the detailed failure block. The spec reporter also numbers failures inline
// (intermixed with passing "✓" lines), so the detail block is parsed only from
// this marker onward to avoid mistaking those progress lines for headers.
var mochaFailingRe = regexp.MustCompile(`^\s*\d+ failing\b`)

// parseMochaFailures extracts failing tests from Mocha reporter output
// (`mocha`/`npx mocha`). Each entry in the trailing "N failing" block opens with
// a "  N) <title>" header; Mocha prints the full test title across one or more
// increasingly-indented lines, the last ending in a colon, so the segments are
// joined (and the trailing colon dropped) to form the name. The detail is the
// first non-empty line after the title — the assertion or thrown-exception
// message — located before the next numbered header so an entry without a body
// does not borrow the following one's.
func parseMochaFailures(output string) []testFailure {
	lines := splitLines(output)
	// Scan only from the "N failing" summary onward; the spec reporter's inline
	// progress section numbers failures too, and parsing those would invent
	// entries with mangled titles.
	start := -1
	for i, ln := range lines {
		if mochaFailingRe.MatchString(ln) {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return nil
	}
	lines = lines[start:]
	var failures []testFailure
	seen := map[string]bool{}
	for i := 0; i < len(lines); i++ {
		m := mochaHeaderRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		// Collect the title segments: the header text plus any following non-empty
		// lines up to and including the one ending in a colon (the test's own name).
		segs := []string{strings.TrimSpace(m[1])}
		j := i + 1
		titleDone := strings.HasSuffix(segs[0], ":")
		for ; !titleDone && j < len(lines); j++ {
			if mochaHeaderRe.MatchString(lines[j]) {
				break
			}
			t := strings.TrimSpace(lines[j])
			if t == "" {
				continue
			}
			segs = append(segs, t)
			if strings.HasSuffix(t, ":") {
				titleDone = true
				j++
				break
			}
		}
		name := strings.TrimSpace(strings.TrimRight(strings.Join(segs, " "), ":"))
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		f := testFailure{Name: name}
		// The assertion/exception message is the first non-empty line after the
		// title, before the next numbered header.
		for ; j < len(lines); j++ {
			if mochaHeaderRe.MatchString(lines[j]) {
				break
			}
			if t := strings.TrimSpace(lines[j]); t != "" {
				f.Detail = t
				break
			}
		}
		failures = append(failures, f)
	}
	return failures
}

// maxSummarizedFailures bounds how many failed tests the appended summary block
// lists. A suite that breaks wholesale (a bad import, a renamed symbol) can fail
// hundreds of tests at once; listing every one would bury the rest of the output
// and the model's context in near-identical lines. The full set is always kept
// in Metadata[MetadataTestFailures], so nothing is lost — only the inline render
// is truncated, with a trailing "... and N more" marker so the elision is
// explicit rather than silent.
const maxSummarizedFailures = 50

// summarizeTestFailures renders a compact, agent-friendly block listing the
// failed tests (and their detail line when known). At most maxSummarizedFailures
// entries are shown; any beyond that are collapsed into a "... and N more" line.
// Returns "" for no failures.
func summarizeTestFailures(failures []testFailure) string {
	if len(failures) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[test failures: %d]", len(failures))
	shown := failures
	if len(shown) > maxSummarizedFailures {
		shown = shown[:maxSummarizedFailures]
	}
	for _, f := range shown {
		if f.Detail != "" {
			fmt.Fprintf(&b, "\n  %s — %s", f.Name, f.Detail)
		} else {
			fmt.Fprintf(&b, "\n  %s", f.Name)
		}
	}
	if remaining := len(failures) - len(shown); remaining > 0 {
		fmt.Fprintf(&b, "\n  ... and %d more", remaining)
	}
	return b.String()
}
