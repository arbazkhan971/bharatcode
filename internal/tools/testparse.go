package tools

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
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
	case runnerTox:
		return parseToxFailures(output)
	case runnerJest:
		return parseJestFailures(output)
	case runnerCargo:
		return parseCargoTestFailures(output)
	case runnerNextest:
		return parseNextestFailures(output)
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
	case runnerCTest:
		return parseCTestFailures(output)
	case runnerPlaywright:
		return parsePlaywrightFailures(output)
	case runnerMinitest:
		return parseMinitestFailures(output)
	case runnerDart:
		return parseDartTestFailures(output)
	case runnerJulia:
		return parseJuliaTestFailures(output)
	case runnerScala:
		return parseScalaTestFailures(output)
	case runnerClojure:
		return parseClojureTestFailures(output)
	case runnerGinkgo:
		return parseGinkgoFailures(output)
	case runnerFoundry:
		return parseFoundryTestFailures(output)
	case runnerJasmine:
		return parseJasmineFailures(output)
	case runnerPester:
		return parsePesterFailures(output)
	case runnerGTest:
		return parseGTestFailures(output)
	case runnerRobot:
		return parseRobotFailures(output)
	case runnerCucumber:
		return parseCucumberFailures(output)
	case runnerBehave:
		return parseBehaveFailures(output)
	case runnerKarma:
		return parseKarmaFailures(output)
	case runnerGotestsum:
		return parseGotestsumFailures(output)
	case runnerBusted:
		return parseBustedFailures(output)
	case runnerHaskell:
		return parseHaskellFailures(output)
	case runnerBazel:
		return parseBazelFailures(output)
	case runnerCrystal:
		return parseCrystalFailures(output)
	case runnerZig:
		return parseZigTestFailures(output)
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
	runnerCTest
	runnerPlaywright
	runnerMinitest
	runnerNextest
	runnerDart
	runnerJulia
	runnerScala
	runnerClojure
	runnerFoundry
	runnerGinkgo
	runnerTox
	runnerJasmine
	runnerPester
	runnerGTest
	runnerRobot
	runnerCucumber
	runnerBehave
	runnerKarma
	runnerGotestsum
	runnerBusted
	runnerHaskell
	runnerBazel
	runnerCrystal
	runnerZig
)

// Word-boundary matchers for the command-name runners, so "go testing the
// waters" or "cargo testbed" do not misclassify as test invocations. \b after
// "test" requires a following non-word char (space, flag, end) — "go test" and
// "go test ./..." match, "go testing" does not.
var (
	goTestRe    = regexp.MustCompile(`\bgo test\b`)
	cargoTestRe = regexp.MustCompile(`\bcargo test\b`)
	// "cargo nextest run" drives cargo-nextest, a third-party Rust runner whose
	// per-test "FAIL [   0.005s] <binary> <test>" output differs from libtest's
	// "test <name> ... FAILED". cargoTestRe ("cargo test") never matches a
	// "cargo nextest" invocation, so this is checked first to claim it for the
	// nextest parser. \b after "nextest" admits "cargo nextest run --no-fail-fast"
	// while keeping prose like "cargo nextesting" from matching.
	nextestRe = regexp.MustCompile(`\bcargo nextest\b`)
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
	// "prove" is Perl's standard Test::Harness driver; run verbose ("prove -v")
	// it streams each test file's raw TAP, including the "not ok N - <desc>"
	// failure lines and "#"-comment diagnostics the TAP parser keys on, so it
	// shares that parser. \bprove\b admits "prove", "prove -lv t/", and
	// "perl Build.PL && prove" while keeping prose like "approved" / "improved"
	// — where "prove" is not at a word boundary — from matching.
	proveRe = regexp.MustCompile(`\bprove\b`)
	// "swift test" drives SwiftPM's test runner — XCTest, the newer Swift Testing
	// (@Test) framework, or both in one run; \b after "test" keeps prose like
	// "swift testing guide" from matching while admitting flags and paths
	// ("swift test --filter FooTests").
	swiftTestRe = regexp.MustCompile(`\bswift test\b`)
	// "forge test" drives Foundry's Solidity test runner; \bforge test\b admits
	// "forge test" and "forge test --match-test X" while keeping prose like
	// "forge testbed" — where "test" runs into the next word — from matching.
	forgeTestRe = regexp.MustCompile(`\bforge test\b`)
	// "ctest" drives CMake's test runner; \bctest\b admits "ctest", "ctest -R Foo",
	// and "cmake --build . && ctest" while keeping prose like "subjects of the
	// ctesting" — and the unrelated "go test"/"cargo test" runners, which never
	// contain the "ctest" substring at a word boundary — from matching.
	ctestRe = regexp.MustCompile(`\bctest\b`)
	// Minitest is Ruby's stdlib test framework and the Rails default. It has no
	// single binary, so it is recognized by its idiomatic invocations: "rails
	// test" / "rake test" (Rails/Rake task), a literal "minitest", or the classic
	// "ruby -Itest ..." load-path runner. \b keeps prose ("rake testing notes")
	// and the unrelated "go test"/"cargo test" runners — which never contain these
	// substrings at a word boundary — from matching. Checked after rspecRe so a
	// "bundle exec rspec" invocation (RSpec, not Minitest) is classified first.
	minitestRe = regexp.MustCompile(`\brails test\b|\brake test\b|\bminitest\b|\bruby\b.*\s-itest\b`)
	// "nose2" and "nosetests" (classic nose) are Python runners built on the
	// stdlib unittest framework: both render results through unittest's
	// TextTestResult, so their failure output is the same "FAIL:/ERROR: <id>"
	// block the unittest parser handles. \b keeps prose like "a diagnosis2 step"
	// from matching while admitting "nose2", "python -m nose2", and "nosetests".
	// The bare package name "nose" is intentionally not matched, since it appears
	// as an ordinary word far more often than as a runner invocation.
	noseRe = regexp.MustCompile(`\bnose2\b|\bnosetests\b`)
	// Julia's stdlib Test.jl has no dedicated binary; it is driven either by
	// running a test script ("julia test/runtests.jl") or via the package manager
	// ("julia -e 'using Pkg; Pkg.test()'"). Both carry "julia" plus a distinctive
	// "runtests.jl" or "pkg.test" token, which keeps ordinary "julia script.jl"
	// invocations (not test runs) from matching.
	juliaRe = regexp.MustCompile(`\bjulia\b.*\bruntests\.jl\b|\bjulia\b.*\bpkg\.test\b`)
	// "sbt" drives Scala builds; "sbt test"/"sbt testOnly" run ScalaTest (or
	// specs2/MUnit), whose default reporter marks each failing example with a
	// "- <name> *** FAILED ***" line under the "[info]"/"[error]" sbt log prefix.
	// \bsbt\b admits "sbt test", "sbt 'testOnly *Spec'", and "./sbt test" while
	// keeping prose like "subtle changes" — and the unrelated "go test"/"cargo
	// test" runners, which never contain "sbt" at a word boundary — from matching.
	sbtRe = regexp.MustCompile(`\bsbt\b`)
	// Clojure's stdlib clojure.test has no dedicated binary; it is driven by
	// "lein test" (Leiningen), "kaocha"/"lein kaocha", or the tools.deps CLI with
	// a test alias ("clojure -M:test", "clj -X:test"). The ":test" alias token and
	// the "lein test"/"kaocha" invocations are distinctive enough to avoid matching
	// a plain "clojure script.clj" run that is not a test invocation. \b keeps
	// prose like "include the latest features" from matching.
	clojureTestRe = regexp.MustCompile(`\blein test\b|\bkaocha\b|\bcl(?:j|ojure)\b[^&|;]*:test\b`)
	// `ginkgo`, `ginkgo run`, `ginkgo -r`, or `go run .../ginkgo` drive the Ginkgo
	// BDD runner. \bginkgo\b admits the wrapper invocations while keeping prose
	// like "the ginkgo tree" from matching only at a word boundary — the same
	// command-as-prose risk the other command-name runners accept. A "ginkgo"
	// invocation never contains "go test" at a word boundary, so it must be
	// classified before the plain `go test` runner claims it (Ginkgo's
	// "Summarizing N Failures" block differs from the "--- FAIL:" lines goTestRe
	// keys on).
	ginkgoRe = regexp.MustCompile(`\bginkgo\b`)
	// `tox` and `nox` are Python automation tools that, in the overwhelmingly
	// common case, run pytest (or stdlib unittest) inside their managed
	// environments and relay that runner's output verbatim — including the
	// "FAILED <id> - <msg>" / "FAIL: <id>" lines the Python parsers key on. tox's
	// and nox's own per-environment summary lines ("py311: FAILED ...") are
	// indented or prefixed and so do not collide with those column-0 markers. \b
	// keeps prose like "intoxicated" / "obnoxious" / "Knox" from matching while
	// admitting "tox", "tox -e py311", "nox -s tests", and "uvx tox". A "pytest"
	// or "python -m unittest" invocation is classified by its own (earlier) case,
	// so this only claims bare tox/nox driver commands.
	toxRe = regexp.MustCompile(`\b(?:tox|nox)\b`)
	// `jasmine`/`npx jasmine`/`node_modules/.bin/jasmine` drive Jasmine's
	// standalone CLI, whose default ConsoleReporter closes with a "Failures:"
	// block numbering each failed spec ("1) <full spec name>") and printing the
	// assertion under an indented "Message:" line. \bjasmine\b admits the wrapper
	// invocations while matching only at a word boundary — accepting the same
	// command-as-prose risk the other command-name runners accept. A "jasmine"
	// invocation never contains "jest"/"vitest" at a
	// word boundary, so it is classified before the jest runner claims it (jest's
	// embedded jasmine2 engine is unrelated to the standalone CLI's report shape).
	jasmineRe = regexp.MustCompile(`\bjasmine\b`)
	// `Invoke-Pester`/`pester`/`pwsh -c Invoke-Pester` drive Pester, PowerShell's
	// dominant test framework, whose detailed console output marks each failing
	// test with an indented "[-] <name> <duration>" line and prints the assertion
	// on the indented line(s) beneath. \bpester\b admits "invoke-pester" (the "-"
	// is a word boundary) and the bare "pester" wrapper while keeping prose like
	// "trumpeters tune up" — which contains no "pester" at a word boundary — from
	// matching. A Pester invocation carries none of the other runners' substrings.
	pesterRe = regexp.MustCompile(`\bpester\b`)
	// `robot tests/`, `python -m robot`, and the parallel-runner `pabot` drive
	// Robot Framework, whose console reporter renders each test as a "<name> ...
	// | FAIL |" row and prints the failure message on the line beneath. \b admits
	// "robot tests", "python -m robot suite.robot", and "pabot --processes 4"
	// while keeping prose like "the robotics demo" — and the unrelated runners,
	// which never contain "robot"/"pabot" at a word boundary — from matching.
	robotRe = regexp.MustCompile(`\b(?:robot|pabot)\b`)
	// `cucumber`/`bundle exec cucumber` (Ruby) and `cucumber-js`/`npx cucumber-js`
	// (JavaScript) drive Cucumber, the dominant Gherkin BDD runner. Both reporters
	// close a failing run with a "Failing [Ss]cenarios:" block listing each failed
	// scenario as a re-runnable "cucumber[-js] <file>.feature:<line>[ # Scenario:
	// <name>]" line. \bcucumber\b admits both the Ruby and JS wrappers while keeping
	// prose like "echo about cucumbers" — where "cucumber" runs into "s" and so sits
	// at no trailing word boundary — from matching. A Cucumber invocation carries
	// none of the other runners' substrings.
	cucumberRe = regexp.MustCompile(`\bcucumber\b`)
	// `behave`/`python -m behave` drive behave, the dominant Python Gherkin BDD
	// runner, whose default reporter closes a failing run with a "Failing
	// scenarios:" block listing each failed scenario as an indented
	// "<file>.feature:<line>  <scenario name>" line (two spaces separating the
	// re-runnable location from the name). \bbehave\b admits "behave",
	// "python -m behave", and "behave features/" while keeping prose like
	// "misbehave" / "behaves oddly" — where "behave" sits at no surrounding word
	// boundary — from matching. A behave invocation carries none of the other
	// runners' substrings, and shares no command name with Ruby/JS Cucumber.
	behaveRe = regexp.MustCompile(`\bbehave\b`)
	// `karma start`/`npx karma`/`ng test` drive Karma (the dominant Angular test
	// runner), whose progress/dots reporter prints each failing spec as
	// "<browser> <suite> <test> FAILED" with the assertion on the tab-indented
	// line(s) beneath. \bkarma\b admits "karma start karma.conf.js" and the bare
	// wrapper; \bng test\b admits Angular's "ng test"/"ng test --watch=false" (its
	// default builder runs Karma) while keeping prose like "running test data" —
	// where "ng" sits mid-word — from matching. A Karma invocation carries none of
	// the other JS runners' substrings, so it is classified before them.
	karmaRe = regexp.MustCompile(`\bkarma\b|\bng test\b`)
	// `busted`/`busted spec/` drives Busted, the dominant Lua test framework,
	// whose default (utfTerminal) reporter closes a failing run with a block per
	// failure: a "Failure → <file> @ <line>" / "Error → <file> @ <line>" header
	// (the plainTerminal handler spells the arrow "->"), the full test description
	// on the next line, and the "<file>:<line>: <message>" assertion beneath.
	// \bbusted\b admits "busted", "busted spec/", and "busted --output=TAP foo"
	// while keeping prose like "the adjusted plan" — where "busted" sits at no
	// word boundary — from matching. A busted invocation carries none of the other
	// runners' substrings.
	bustedRe = regexp.MustCompile(`\bbusted\b`)
	// `stack test`/`cabal test` (and the `cabal v2-test`/`cabal new-test`
	// aliases) drive a Haskell test suite — overwhelmingly hspec, whose reporter
	// closes a failing run with a "Failures:" block numbering each failed example
	// ("N) <description>") with its "<file>.hs:<line>:<col>:" source location on
	// the indented line above. \bstack test\b admits "stack test --fast"; the
	// cabal form requires both "cabal" and a following "test" token at word
	// boundaries, so "cabal test", "cabal v2-test", and "cabal new-test" match
	// while "cabal build" / "cabal run" and prose like "the cabal was tested"
	// (where "test" runs into a following word) do not. The "[^&|;]*" keeps the
	// match within a single command rather than spanning a "cabal build && go
	// test" chain. A Haskell invocation carries none of the other runners'
	// substrings.
	haskellRe = regexp.MustCompile(`\bstack test\b|\bcabal\b[^&|;]*\btest\b`)
	// `bazel test //...`/`bazelisk test`/`bazel coverage` drive Bazel's test
	// runner, whose final summary lists each test target as a right-padded
	// "//pkg:target    FAILED in <time>" row (the per-target log path on the
	// indented line beneath). Both "bazel" and a following "test" (or "coverage",
	// which also runs the tests) token are required at word boundaries, so
	// "bazel test //foo", "bazelisk test", and "bazel coverage //..." match while
	// "bazel build //..." / "bazel run //x" and prose like "the bazel cache" do
	// not. The "[^&|;]*" keeps the match within a single command rather than
	// spanning a "bazel build && go test" chain. A Bazel invocation carries none
	// of the other runners' substrings.
	bazelRe = regexp.MustCompile(`\bbazel(?:isk)?\b[^&|;]*\b(?:test|coverage)\b`)
	// `crystal spec`/`crystal spec spec/` drives Crystal's stdlib spec framework,
	// whose default formatter closes a failing run with a "Failed examples:" block
	// listing each failure as a re-runnable "crystal spec <file>.cr:<line> #
	// <description>" line (the same shape RSpec uses, but with the "crystal spec"
	// prefix). Both "crystal" and a following "spec" token are required at word
	// boundaries, so "crystal spec", "crystal spec spec/foo_spec.cr", and "crystal
	// spec --verbose" match while "crystal build" / "crystal run" and prose like
	// "the crystal specimen" — where "spec" runs into the next word — do not. The
	// "[^&|;]*" keeps the match within a single command rather than spanning a
	// "crystal build && go test" chain. A Crystal invocation carries none of the
	// other runners' substrings (and the bare "spec" never matches \brspec\b).
	crystalSpecRe = regexp.MustCompile(`\bcrystal\b[^&|;]*\bspec\b`)
	// `zig test <file>` and `zig build test` drive Zig's built-in test runner,
	// whose console output marks each failing test with a "Test [N/M] test
	// "<name>"... FAIL (reason)" line. Both "zig" and "test" are required at word
	// boundaries within the same command segment, so "zig test src/main.zig",
	// "zig build test", and "zig test --summary failures" match while
	// "zig build run" (no test token) and prose like "zigzag tested" — where
	// "zig" is not at a word boundary — do not. The [^&|;]* keeps the match
	// within a single command rather than spanning a "zig build && go test" chain.
	zigTestRe = regexp.MustCompile(`\bzig\b[^&|;]*\btest\b`)
	// \bnpm t\b matches "npm t" with a trailing word boundary, covering both the
	// bare "npm t" invocation (npm's built-in alias for "npm test") and "npm t
	// --watch" (with arguments), while excluding "npm typescript" or "npm ta*"
	// where the "t" is followed by another word character. "npm test" is checked
	// first in the jest case so this only needs to catch the short alias.
	npmShortRe = regexp.MustCompile(`\bnpm t\b`)
)

// classifyTestRunner inspects the command string for a known test-runner
// invocation. Matching is lowercased and tolerant of wrappers (env prefixes,
// &&-chains, flags), but uses word boundaries for the command-name runners to
// avoid matching prose that merely contains "go test".
func classifyTestRunner(command string) testRunner {
	c := strings.ToLower(command)
	switch {
	case nextestRe.MatchString(c):
		// `cargo nextest run` drives cargo-nextest, whose reporter prints
		// "FAIL [   0.005s] <binary> <test>" per failure (repeated in the final
		// Summary block). Checked before the libtest `cargo test` runner since a
		// "cargo nextest" invocation never contains "cargo test" at a word boundary.
		return runnerNextest
	case cargoTestRe.MatchString(c):
		return runnerCargo
	case ginkgoRe.MatchString(c):
		// `ginkgo`/`ginkgo run -r` drives the Ginkgo BDD runner, whose report
		// closes with a "Summarizing N Failure(s):" block listing each failed spec
		// as "[FAIL]/[PANIC!]/[TIMEOUT] <full spec text>" with the source location
		// on the line beneath. Checked before the plain `go test` runner: Ginkgo's
		// summary block differs from libtest's "--- FAIL:" lines, and a "ginkgo"
		// invocation never contains "go test" at a word boundary.
		return runnerGinkgo
	case strings.Contains(c, "gotestsum"):
		// `gotestsum` wraps `go test -json` and renders its own human output, whose
		// default (pkgname) format closes with a "=== Failed" block listing each
		// failure as "=== FAIL: <pkg> <Test> (0.00s)". That summary differs from the
		// "--- FAIL:" lines goTestRe keys on, and a "gotestsum" invocation never
		// contains "go test" at a word boundary, so it is claimed here before the
		// plain `go test` runner.
		return runnerGotestsum
	case goTestRe.MatchString(c):
		return runnerGo
	case rspecRe.MatchString(c):
		return runnerRSpec
	case minitestRe.MatchString(c):
		// `rails test`/`rake test`/`ruby -Itest ...` drive Minitest, whose default
		// reporter numbers each failure ("  1) Failure:"/"  1) Error:") and prints
		// the "Class#method [file:line]:" id and assertion/exception message on the
		// lines beneath. Checked after RSpec so a "bundle exec rspec" invocation is
		// not misread, and before the JS/Python runners (a Ruby invocation carries
		// none of their substrings) to guard against future overlap.
		return runnerMinitest
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
	// summary distinct from pytest's, so they get their own parser. `nose2` and
	// `nosetests` build on unittest and emit the same TextTestResult report, so
	// they route to the same parser. Checked before the JS runners since these
	// invocations carry none of their substrings, but kept explicit to guard
	// against future overlap.
	case strings.Contains(c, "unittest"), noseRe.MatchString(c):
		return runnerUnittest
	case toxRe.MatchString(c):
		// `tox`/`nox` drive Python test automation that almost always runs pytest
		// (or stdlib unittest) underneath and relays its output, so the failures are
		// extracted by the same Python parsers. Checked after the explicit pytest and
		// unittest cases so a "tox -e py -- pytest ..." command — which carries the
		// "pytest" substring — is claimed by the more specific pytest case first.
		return runnerTox
	case tapRe.MatchString(c), batsRe.MatchString(c), proveRe.MatchString(c):
		// `prove -v` drives Perl's Test::Harness, relaying each test file's raw TAP
		// ("not ok N - <desc>" lines with "#"-comment diagnostics), so it routes to
		// the shared TAP parser alongside node:test/tape/bats.
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
	case strings.Contains(c, "cypress run"):
		// `cypress run`/`npx cypress run` executes Cypress headlessly with Mocha's
		// spec reporter underneath, so its failures close with the same "N failing"
		// block of numbered "  N) <title>" entries — ending in a colon — that the
		// Mocha parser handles, followed by the assertion message. Routed to that
		// parser rather than duplicating it. Checked before the jest runners since a
		// cypress invocation carries none of their substrings. Only "cypress run"
		// (the headless command that emits this report) matches; "cypress open"
		// launches the interactive GUI and prints no such summary.
		return runnerMocha
	case strings.Contains(c, "hardhat test"):
		// `hardhat test`/`npx hardhat test` runs Hardhat's test task, which drives
		// Mocha with the spec reporter underneath, so its failures close with the
		// same "N failing" block of numbered "  N) <title>" entries — each title
		// ending in a colon — that the Mocha parser handles, followed by the
		// assertion message. Routed to that parser rather than duplicating it.
		// Checked before the jest runners since a hardhat invocation carries none of
		// their substrings, but kept explicit to guard against future overlap. (The
		// other Solidity runner, Foundry's `forge test`, has its own case and shares
		// no command substring with hardhat.)
		return runnerMocha
	case ctestRe.MatchString(c):
		// `ctest` drives CMake's test runner, whose run summary closes with a
		// "The following tests FAILED:" block listing each failure as
		// "<n> - <name> (<status>)". Checked before the JS runners since a ctest
		// invocation carries none of their substrings, but kept explicit to guard
		// against future overlap.
		return runnerCTest
	case strings.Contains(c, "--gtest"):
		// A "--gtest_*" flag (--gtest_filter, --gtest_output, --gtest_color, ...) is
		// a definitive GoogleTest invocation: gtest binaries have no standard command
		// name, so the flag is the only false-positive-free command signal. Its
		// console output marks each failing test with a "[  FAILED  ] <suite>.<test>"
		// line (repeated in a trailing "[  FAILED  ] N tests, listed below:" block).
		// A bare gtest binary run with no flags is not auto-detected — guessing a
		// runner from output alone risks false positives on ordinary logs. Checked
		// before the JS runners since a gtest flag carries none of their substrings,
		// but kept explicit to guard against future overlap.
		return runnerGTest
	case strings.Contains(c, "playwright test"):
		// `playwright test`/`npx playwright test` drives Playwright's runner, whose
		// list/line reporters close with numbered "  N) <file>:<line> › <title>"
		// failure headers (each padded with box-drawing dashes) followed by the
		// error message — a shape none of the other JS runners share. Checked before
		// the jest runners since a "playwright test" invocation carries none of their
		// substrings, but kept explicit to guard against future overlap.
		return runnerPlaywright
	case strings.Contains(c, "dart test"), strings.Contains(c, "flutter test"):
		// `dart test`/`flutter test` drive Dart's package:test runner, whose default
		// (compact) reporter prints a rewritten "MM:SS +P ~S -F: <name> [E]" status
		// line — the "[E]" suffix marking a failing test — followed by the indented
		// assertion/exception block. Checked before the JS runners since a Dart
		// invocation carries none of their substrings, but kept explicit to guard
		// against future overlap.
		return runnerDart
	case juliaRe.MatchString(c):
		// `julia test/runtests.jl` / `julia -e 'using Pkg; Pkg.test()'` drive
		// Julia's stdlib Test.jl, whose report opens each failure with a "Test
		// Failed at <file>:<line>" / "Error During Test at <file>:<line>" header and
		// prints the tested expression on the indented "Expression:" line beneath.
		// Checked before the JS runners since a Julia invocation carries none of
		// their substrings, but kept explicit to guard against future overlap.
		return runnerJulia
	case sbtRe.MatchString(c):
		// `sbt test`/`sbt testOnly` drives ScalaTest (and specs2/MUnit), whose
		// reporter prints each failing example as "- <name> *** FAILED ***" beneath
		// sbt's "[info]"/"[error]" log prefix, with the assertion on the indented
		// line below. Checked before the JS runners since an sbt invocation carries
		// none of their substrings, but kept explicit to guard against future overlap.
		return runnerScala
	case clojureTestRe.MatchString(c):
		// `lein test`/`clojure -M:test`/`clj -X:test`/`kaocha` drive clojure.test,
		// whose default reporter opens each failure with a "FAIL in (<var>)
		// (<file>:<line>)" / "ERROR in (<var>) (<file>:<line>)" header and prints
		// the failing form on an indented "actual:" line beneath. Checked before
		// the JS runners since a Clojure invocation carries none of their
		// substrings, but kept explicit to guard against future overlap.
		return runnerClojure
	case forgeTestRe.MatchString(c):
		// `forge test` drives Foundry's Solidity test runner, whose reporter prints
		// each failing test as "[FAIL: <reason>] <fn>(<args>) (gas: N)" (older
		// builds spell the marker "[FAIL. Reason: <reason>]"). Checked before the JS
		// runners since a forge invocation carries none of their substrings, but
		// kept explicit to guard against future overlap.
		return runnerFoundry
	case jasmineRe.MatchString(c):
		// `jasmine`/`npx jasmine` drives Jasmine's standalone CLI, whose default
		// ConsoleReporter closes with a "Failures:" block of numbered
		// "N) <full spec name>" entries, each followed by an indented "Message:"
		// line carrying the assertion. Checked before the jest runners since a
		// "jasmine" invocation carries none of their substrings, but kept explicit
		// to guard against future overlap.
		return runnerJasmine
	case pesterRe.MatchString(c):
		// `Invoke-Pester`/`pester` drives PowerShell's Pester framework, whose
		// detailed output marks each failing test with an indented "[-] <name>
		// <duration>" line ("[+]" passed, "[!]" skipped) and prints the assertion on
		// the indented line beneath. Checked before the JS runners since a Pester
		// invocation carries none of their substrings, but kept explicit to guard
		// against future overlap.
		return runnerPester
	case robotRe.MatchString(c):
		// `robot`/`pabot`/`python -m robot` drive Robot Framework, whose console
		// reporter renders each test as a "<name> ... | FAIL |" row and prints the
		// failure message on the line beneath; each suite closes with a row of the
		// same shape followed by a "N tests, M passed, K failed" statistics line,
		// which the parser uses to tell suites from tests. Checked before the JS
		// runners since a Robot invocation carries none of their substrings, but
		// kept explicit to guard against future overlap.
		return runnerRobot
	case cucumberRe.MatchString(c):
		// `cucumber`/`cucumber-js` drive Cucumber's Gherkin BDD runner, whose report
		// closes with a "Failing [Ss]cenarios:" block of re-runnable
		// "cucumber <file>.feature:<line> # Scenario: <name>" lines. Checked before
		// the jest runners since a "cucumber" invocation carries none of their
		// substrings, but kept explicit to guard against future overlap.
		return runnerCucumber
	case behaveRe.MatchString(c):
		// `behave`/`python -m behave` drive behave, Python's Gherkin BDD runner,
		// whose report closes with a "Failing scenarios:" block of indented
		// "<file>.feature:<line>  <scenario name>" lines. Checked after Cucumber (a
		// behave invocation never contains "cucumber" at a word boundary) and before
		// the JS runners since a behave invocation carries none of their substrings,
		// but kept explicit to guard against future overlap.
		return runnerBehave
	case karmaRe.MatchString(c):
		// `karma`/`ng test` drive Karma, whose progress reporter marks each failing
		// spec with a "<browser> <suite> <test> FAILED" line and prints the assertion
		// on the tab-indented line(s) beneath. Checked before the jest runners since a
		// Karma invocation carries none of their substrings, but kept explicit to
		// guard against future overlap. (Karma usually drives Jasmine specs, but its
		// reporter output differs from the standalone jasmine CLI's "Failures:" block,
		// so it needs its own parser.)
		return runnerKarma
	case bustedRe.MatchString(c):
		// `busted` drives Lua's Busted framework, whose reporter closes a failing
		// run with a "Failure → <file> @ <line>" / "Error → <file> @ <line>" block
		// naming each failed example, the full description on the next line, and the
		// "<file>:<line>: <message>" assertion beneath. Checked before the JS runners
		// since a busted invocation carries none of their substrings, but kept
		// explicit to guard against future overlap.
		return runnerBusted
	case haskellRe.MatchString(c):
		// `stack test`/`cabal test` drive a Haskell suite (hspec), whose report
		// closes with a "Failures:" block of numbered "N) <description>" entries,
		// each preceded by an indented "<file>.hs:<line>:<col>:" source location.
		// Checked before the JS runners since a Haskell invocation carries none of
		// their substrings, but kept explicit to guard against future overlap. A
		// run that drives tasty instead prints no "Failures:" header, so the parser
		// simply surfaces nothing rather than misreading it.
		return runnerHaskell
	case bazelRe.MatchString(c):
		// `bazel test //...`/`bazelisk test` drive Bazel, whose run summary lists
		// each failed target as "//pkg:target    FAILED in <time>" with the
		// per-target test.log path on the indented line beneath. Checked before the
		// JS runners since a Bazel invocation carries none of their substrings, but
		// kept explicit to guard against future overlap. (Bazel wraps the language's
		// own runner — go test, JUnit, pytest — but only its target-level pass/fail
		// summary is reliably present, so it gets its own parser keyed on the "//"
		// target rows.)
		return runnerBazel
	case crystalSpecRe.MatchString(c):
		// `crystal spec` drives Crystal's stdlib spec framework, whose default
		// formatter closes a failing run with a "Failed examples:" block listing
		// each failure as a re-runnable "crystal spec <file>.cr:<line> #
		// <description>" line — the same summary shape RSpec uses. Checked before
		// the JS runners since a Crystal invocation carries none of their
		// substrings, but kept explicit to guard against future overlap. (The bare
		// "spec" token never matches the \brspec\b RSpec runner, so the earlier
		// RSpec case does not claim it.)
		return runnerCrystal
	case zigTestRe.MatchString(c):
		// `zig test <file>` and `zig build test` drive Zig's built-in test runner,
		// whose console output marks each failing test with a "Test [N/M] test
		// "<name>"... FAIL (reason)" line. Checked before the JS runners since a
		// zig invocation carries none of their substrings, but kept explicit to
		// guard against future overlap.
		return runnerZig
	// `bun run test` invokes an arbitrary npm-style test script via bun's package
	// manager (the same role npm/yarn/pnpm play), so it is classified alongside
	// those runners rather than as `runnerBun` (bun's native test runner). `bun
	// test` — bun's own runner, which uses a "✗" glyph distinct from jest's "✕" —
	// is already claimed above and never reaches this case.
	case strings.Contains(c, "jest"), strings.Contains(c, "vitest"),
		strings.Contains(c, "npm test"), npmShortRe.MatchString(c),
		strings.Contains(c, "npm run test"), strings.Contains(c, "yarn test"),
		strings.Contains(c, "yarn run test"), strings.Contains(c, "pnpm test"),
		strings.Contains(c, "pnpm run test"), strings.Contains(c, "bun run test"):
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
	// (or whose test setup or vet check failed) rather than a failing assertion.
	// Go emits no "--- FAIL:" line in these cases, so without separate handling
	// a failed run would surface zero structured failures. Group 2 is the reason
	// bracket content: "build failed", "setup failed", or "vet failed".
	goBuildFailRe = regexp.MustCompile(`^FAIL\s+(\S+) \[(build failed|setup failed|vet failed)\]$`)
	// A compiler/vet diagnostic at column 0, e.g. "./foo.go:10:2: undefined: x"
	// or an absolute path. Used as the detail for a build failure. Indented
	// assertion details are matched by goDetailRe instead, so the two do not
	// collide.
	goCompileErrRe = regexp.MustCompile(`^\S*\.go:\d+:\d+: .+`)
	// A goroutine-stack frame naming a test function, e.g.
	// "github.com/x/y.TestFoo(0xc000102000)" (or "...TestFoo.func1(...)" for a
	// closure within it). The captured group is the test name. Used to attribute a
	// binary-aborting panic — which prints no "--- FAIL:" line — to the test whose
	// frame appears in the panic's stack trace.
	goPanicStackTestRe = regexp.MustCompile(`\.(Test[^.\s(]*)(?:\.func\d+)?\(`)
	// "panic: test timed out after 30s" — the panic Go emits when the test binary
	// exceeds its -timeout deadline. Group 1 captures the duration string ("30s").
	// Distinct from the generic goPanicRe so parseGoTestTimeoutRunning can pair it
	// with the "running tests:" block that follows without affecting other parsers.
	goTimeoutPanicRe = regexp.MustCompile(`^panic: test timed out after (.+)$`)
	// "\tTestFoo (9.5s)" — one entry in the "running tests:" block Go prints
	// immediately after a timeout panic, before the goroutine dump. Group 1 is the
	// test name. The leading tab is literal; the name runs to the first space.
	goRunningTestRe = regexp.MustCompile(`^\t(Test[^\s(]+) \(`)
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
	// A test that panics without recovering crashes the whole test binary before
	// Go can print a "--- FAIL:" line for it, so the loop above never captures it
	// — Go emits a bare column-0 "panic: <msg>" followed by the goroutine stack.
	// Recover the panicking test from that stack so it still surfaces.
	failures = append(failures, parseGoTestPanics(lines, failures)...)
	// Parallel tests blocked on IO at timeout may have no visible Test* frame in
	// the goroutine dump; the "running tests:" block printed before the dump names
	// them directly. Supply any names not already attributed by parseGoTestPanics.
	failures = append(failures, parseGoTestTimeoutRunning(lines, failures)...)
	return failures
}

// parseGoTestPanics recovers tests that aborted the binary with an unrecovered
// panic. Such a panic prints a column-0 "panic: <msg>" line with no preceding
// "--- FAIL:" marker, so parseGoTestFailures' main loop misses it. For each such
// panic this scans the goroutine stack that follows for the first frame naming a
// Test function and attributes the panic to it, using "panic: <msg>" as the
// detail. Tests already recorded by the caller (a "--- FAIL:" that also printed a
// panic) are skipped so they are not double-counted, as are panics whose stack
// names no test (e.g. one raised in an unrelated background goroutine).
func parseGoTestPanics(lines []string, existing []testFailure) []testFailure {
	seen := make(map[string]bool, len(existing))
	for _, f := range existing {
		seen[f.Name] = true
	}
	var out []testFailure
	for i := 0; i < len(lines); i++ {
		p := goPanicRe.FindStringSubmatch(lines[i])
		if p == nil {
			continue
		}
		// Find the panicking test in the stack frames that follow, stopping at the
		// next top-level panic so a recovered re-panic's stack is not rescanned.
		name := ""
		for j := i + 1; j < len(lines); j++ {
			if goPanicRe.MatchString(lines[j]) {
				break
			}
			if m := goPanicStackTestRe.FindStringSubmatch(lines[j]); m != nil {
				name = m[1]
				break
			}
		}
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, testFailure{Name: name, Detail: "panic: " + strings.TrimSpace(p[1])})
	}
	return out
}

// parseGoTestTimeoutRunning supplements parseGoTestPanics for -timeout panics.
// When `go test -timeout` is exceeded Go prints a "running tests:" block that
// lists every in-flight test by name before dumping goroutine stacks. Tests
// blocked on a syscall (or in non-Go code) do not have a runnable goroutine,
// so their Test* function never appears in the stack dump and parseGoTestPanics
// misses them. This function extracts those names from the "running tests:" list
// and adds any that are not already in existing, using "panic: test timed out
// after Xs" as the detail — identical to what parseGoTestPanics would have used
// — so the detail is consistent regardless of which path attributed the failure.
func parseGoTestTimeoutRunning(lines []string, existing []testFailure) []testFailure {
	seen := make(map[string]bool, len(existing))
	for _, f := range existing {
		seen[f.Name] = true
	}
	var out []testFailure
	for i := 0; i < len(lines); i++ {
		m := goTimeoutPanicRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		detail := "panic: test timed out after " + m[1]
		// The "running tests:" header must immediately follow the panic line.
		i++
		if i >= len(lines) || strings.TrimSpace(lines[i]) != "running tests:" {
			continue
		}
		i++
		for ; i < len(lines); i++ {
			tm := goRunningTestRe.FindStringSubmatch(lines[i])
			if tm == nil {
				break
			}
			name := tm[1]
			if seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, testFailure{Name: name, Detail: detail})
		}
	}
	return out
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
//
// When `-timeout` fires the timeout panic and "running tests:" block arrive as
// package-scoped output events; there are no test-level "fail" events. The
// hanging tests are recovered from the "running tests:" block, mirroring the
// text parser's parseGoTestTimeoutRunning logic.
func parseGoTestJSONFailures(output string) []testFailure {
	var order []string
	failed := map[string]bool{}
	detail := map[string]string{}
	// Per-package state for surfacing build failures and timeout-hanging tests:
	// the count of failed tests (to suppress the package entry once individual
	// tests reported), the first compiler diagnostic seen, all package-scoped
	// output lines (for timeout recovery), and the order packages first failed in.
	testsInPkg := map[string]int{}
	compileErrByPkg := map[string]string{}
	pkgOutputLines := map[string][]string{}
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
				if ev.Package != "" {
					// Collect package-scoped lines for build-failure and timeout recovery.
					pkgOutputLines[ev.Package] = append(pkgOutputLines[ev.Package],
						strings.TrimRight(ev.Output, "\r\n"))
					// Capture the first compiler diagnostic in case this package
					// turns out to be a build failure.
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
	// Append build failures / timeout-hanging tests for packages that failed.
	for _, pkg := range pkgOrder {
		// Surface a build failure only when no individual tests reported: a
		// package that had test-level failures already carries diagnostic value.
		if testsInPkg[pkg] == 0 {
			if ce := compileErrByPkg[pkg]; ce != "" {
				failures = append(failures, testFailure{Name: pkg + " [build failed]", Detail: ce})
				continue
			}
		}
		// Recover tests that were killed by a -timeout panic. The timeout panic
		// and "running tests:" block arrive as package-scoped output events with
		// no test-level "fail" counterparts; reuse the text-parser logic on the
		// collected lines. Pass existing failures so already-reported tests are
		// not double-counted. This runs even when some tests already reported
		// failures: a -timeout run can have both a test that asserted first and
		// other tests still running when the binary was killed.
		if hung := parseGoTestTimeoutRunning(pkgOutputLines[pkg], failures); len(hung) > 0 {
			failures = append(failures, hung...)
		}
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

// gotestsum's default (pkgname) reporter closes a run with a "=== Failed" block
// printing each failed test as "=== FAIL: <pkg> <Test> (0.00s)" — the package is
// the module-relative path, the test name a single token (Go renders subtest
// spaces as underscores), and an optional " (re-run N)" suffix precedes the
// "(<elapsed>s)" timing. Group 1 is the package, group 2 the test.
var gotestsumFailRe = regexp.MustCompile(`^=== FAIL: (\S+) (\S+)(?: \(re-run \d+\))? \([0-9.]+s\)$`)

// parseGotestsumFailures extracts failures from `gotestsum` output. Each
// "=== FAIL: <pkg> <Test> (..s)" summary line names a failed test (Name is
// "<pkg> <Test>" so two packages' identically-named tests stay distinct); the
// detail is the first assertion ("file.go:line:") or panic line in the test's
// output that gotestsum reprints beneath, before the next "=== " header. When no
// such summary is present — gotestsum run with the summary hidden or a
// standard-verbose/quiet format — it falls back to the plain `go test` parser,
// which handles the raw "--- FAIL:" lines and the -json stream gotestsum consumes.
func parseGotestsumFailures(output string) []testFailure {
	lines := splitLines(output)
	var failures []testFailure
	for i := 0; i < len(lines); i++ {
		m := gotestsumFailRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		f := testFailure{Name: m[1] + " " + m[2]}
		for j := i + 1; j < len(lines); j++ {
			if strings.HasPrefix(lines[j], "=== ") {
				break
			}
			if d := goDetailRe.FindStringSubmatch(lines[j]); d != nil {
				f.Detail = strings.TrimSpace(d[1])
				break
			}
			if p := goPanicRe.FindStringSubmatch(strings.TrimSpace(lines[j])); p != nil {
				f.Detail = "panic: " + strings.TrimSpace(p[1])
				break
			}
		}
		failures = append(failures, f)
	}
	if len(failures) > 0 {
		return failures
	}
	return parseGoTestFailures(output)
}

// "FAILED tests/test_x.py::test_y - AssertionError: ..." (pytest short summary).
// "ERROR" lines appear here too, for collection/fixture/teardown errors that
// never reach an assertion; both are actionable failures the agent must see.
// The node id is a non-space token optionally followed by a "[...]" parameter
// section: parametrized ids carry the param values in brackets, and those values
// routinely contain spaces ("test_x[a b]") or even the " - " detail separator
// ("test_x[1 - 2]"), so the bracket span is matched with [^\]]* rather than \S+
// to keep the whole id together before the optional " - <message>" detail.
var pytestFailRe = regexp.MustCompile(`^(?:FAILED|ERROR) (\S+?(?:\[[^\]]*\])?)(?: - (.*))?$`)

// "tests/test_x.py::test_y FAILED" (pytest verbose, no summary). The id may end
// in a "[...]" parameter section whose values contain spaces, so the bracket
// span is matched separately from the space-terminated "<file>::<test>" head.
var pytestVerboseRe = regexp.MustCompile(`^(\S+::\S+?(?:\[[^\]]*\])?) (?:FAILED|ERROR)\b`)

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

// The footer stdlib unittest prints after every run ("Ran 3 tests in 0.001s").
// pytest never emits it, so it reliably tells the two Python runners apart in
// tox/nox output — and notably keeps unittest's own "FAILED (failures=1)"
// summary line from being misread as a pytest "FAILED <node-id>" entry.
var unittestRanRe = regexp.MustCompile(`(?m)^Ran \d+ tests? in `)

// parseToxFailures extracts failing tests from `tox`/`nox` output. Both tools
// run a Python test runner inside a managed environment and stream its output,
// so the failures are whatever that runner printed. A stdlib-unittest run is
// recognized by its "Ran N tests in ..." footer and parsed as unittest;
// everything else is treated as pytest (the dominant choice), whose
// "FAILED <id> - <msg>" summary lines the pytest parser handles. Returns nil
// when no failure markers appear.
func parseToxFailures(output string) []testFailure {
	if unittestRanRe.MatchString(output) {
		return parseUnittestFailures(output)
	}
	return parsePytestFailures(output)
}

var (
	// Jest/vitest mark a failed test with "✕" or "×"; trim a trailing "(N ms)".
	jestFailRe = regexp.MustCompile(`^\s*[✕×] (.+?)(?:\s*\(\d+\s*ms\))?$`)
	jestTimeRe = regexp.MustCompile(`\s*\(\d+\s*ms\)\s*$`)
	// "  ● Calculator › subtracts correctly" — the header opening a detailed
	// failure block. jest/vitest print the full "<suite> › <test>" path here; its
	// leaf segment (after the last " › ") matches the bare test name printed on
	// the "✕ <test>" summary line, so it keys the detail lookup.
	jestDetailHeaderRe = regexp.MustCompile(`^\s*●\s+(.+\S)\s*$`)
	// " FAIL  src/calc.test.ts > Calculator > subtracts" — vitest's per-failure
	// block header. Unlike jest (which uses "● <suite> › <test>" headers and a
	// bare "FAIL <file>" line with no ">"), vitest opens each failed test's block
	// with "FAIL <file> > <suite> > <test>", using " > " throughout. The trailing
	// "> "-delimited segment is the leaf test name, matching the inline "× <test>"
	// summary line; requiring a ">" keeps jest's file-level "FAIL <file>" line
	// (which has none) from matching.
	vitestFailHeaderRe = regexp.MustCompile(`^\s*FAIL\s+(.+>.+\S)\s*$`)
)

// detailHeaderLeaf returns the leaf test name when line opens a jest ("● …") or
// vitest ("FAIL <file> > … > <test>") failure block, and reports whether it did.
// Both runners print the full suite path in the header; the leaf segment (after
// the last "›"/">" separator) matches the bare name on the inline "✕ <test>"
// summary line, so it keys the detail lookup.
func detailHeaderLeaf(line string) (string, bool) {
	if m := jestDetailHeaderRe.FindStringSubmatch(line); m != nil {
		name := strings.TrimSpace(m[1])
		if idx := strings.LastIndex(name, "›"); idx >= 0 {
			name = strings.TrimSpace(name[idx+len("›"):])
		}
		return name, name != ""
	}
	if m := vitestFailHeaderRe.FindStringSubmatch(line); m != nil {
		name := strings.TrimSpace(m[1])
		if idx := strings.LastIndex(name, ">"); idx >= 0 {
			name = strings.TrimSpace(name[idx+1:])
		}
		return name, name != ""
	}
	return "", false
}

// parseJestFailures collects the failing tests jest and vitest report. Each
// "✕ <name>" summary line names a failure; vitest also opens a detailed block
// with "FAIL <file> > <suite> > <name>", so those headers seed failures too (a
// vitest reporter may omit the inline "×" line). The assertion message from the
// matching "● <suite> › <name>" (jest) or "FAIL … > <name>" (vitest) block is
// attached when present. Names are deduplicated by leaf so the same test counted
// from both its summary line and its block header yields a single entry.
func parseJestFailures(output string) []testFailure {
	lines := splitLines(output)
	details := jestFailureDetails(lines)
	var failures []testFailure
	seen := map[string]bool{}
	add := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		failures = append(failures, testFailure{Name: name, Detail: details[name]})
	}
	for _, ln := range lines {
		if m := jestFailRe.FindStringSubmatch(ln); m != nil {
			add(strings.TrimSpace(jestTimeRe.ReplaceAllString(m[1], "")))
			continue
		}
		if name, ok := vitestFailHeaderLeaf(ln); ok {
			add(name)
		}
	}
	return failures
}

// vitestFailHeaderLeaf returns the leaf test name when line is a vitest
// "FAIL <file> > … > <test>" block header. It is the failure-seeding counterpart
// to detailHeaderLeaf, which also recognizes jest's "● …" headers (those always
// have a paired "✕" summary line, so seeding from them would double-count).
func vitestFailHeaderLeaf(line string) (string, bool) {
	m := vitestFailHeaderRe.FindStringSubmatch(line)
	if m == nil {
		return "", false
	}
	name := strings.TrimSpace(m[1])
	if idx := strings.LastIndex(name, ">"); idx >= 0 {
		name = strings.TrimSpace(name[idx+1:])
	}
	return name, name != ""
}

// jestFailureDetails maps each failing test's leaf name to the first assertion or
// exception line in its "● <suite> › <name>" (jest) or "FAIL <file> > … > <name>"
// (vitest) block, before the next such header. The leaf name (the segment after
// the last separator) is used because the inline "✕ <name>" summary line prints
// only that segment. Whole-file "● Test suite failed to run" blocks produce an
// entry with no matching summary line, which is harmless.
func jestFailureDetails(lines []string) map[string]string {
	details := map[string]string{}
	for i := 0; i < len(lines); i++ {
		name, ok := detailHeaderLeaf(lines[i])
		if !ok {
			continue
		}
		if _, ok := details[name]; ok {
			continue // keep the first detail per leaf name
		}
		for j := i + 1; j < len(lines); j++ {
			if _, isHeader := detailHeaderLeaf(lines[j]); isHeader {
				break
			}
			if t := strings.TrimSpace(lines[j]); t != "" {
				details[name] = t
				break
			}
		}
	}
	return details
}

var (
	// "test tests::it_works ... FAILED" (cargo libtest), and the doctest variant
	// "test src/lib.rs - add (line 5) ... FAILED", whose name carries spaces. The
	// name is captured non-greedily up to the terminal " ... FAILED" so both the
	// space-free unit-test path and the spaced doctest path are recognized; the
	// "$" anchor and the literal " ... FAILED" suffix keep the libtest run-summary
	// ("test result: FAILED. ...", which has no " ... ") from matching.
	cargoFailRe = regexp.MustCompile(`^test (.+?) \.\.\. FAILED$`)
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
// the matching "thread '<name>' panicked at ..." detail when present. Doctest
// failures ("test src/lib.rs - add (line 5) ... FAILED") are captured too; their
// panic is reported under thread 'main' rather than the test name, so they
// surface with the name but no paired detail. When the crate fails to compile,
// cargo emits no "... FAILED" lines, so a "could not compile `crate` ..." marker
// is surfaced as a "[build failed]" entry instead, with the first rustc
// diagnostic as its detail.
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
	// "        FAIL [   0.005s] my-crate tests::it_works" — cargo-nextest's
	// per-failure line. Group 1 is the binary id (package "::" target, no
	// spaces), group 2 the libtest test path. TIMEOUT marks a test killed for
	// exceeding its slow-timeout, which nextest also counts as a failure. The
	// same line is reprinted in the trailing "Summary" block, so callers dedupe.
	nextestFailRe = regexp.MustCompile(`^\s*(?:FAIL|TIMEOUT)\s+\[[^\]]*\]\s+(\S+)\s+(\S+)\s*$`)
)

// parseNextestFailures collects cargo-nextest "FAIL [..] <binary> <test>" lines
// (Name is "<binary> <test>") and attaches the matching "thread '<test>'
// panicked at ..." detail when present, reusing the libtest panic matcher since
// nextest relays the same captured panic line. The failure line is emitted both
// inline and in the closing Summary block, so duplicates are dropped. A crate
// that fails to compile emits no FAIL lines, so a "could not compile `crate`"
// marker is surfaced as a "[build failed]" entry as in parseCargoTestFailures.
func parseNextestFailures(output string) []testFailure {
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
	seen := map[string]bool{}
	for _, ln := range lines {
		if m := nextestFailRe.FindStringSubmatch(ln); m != nil {
			name := m[1] + " " + m[2]
			if seen[name] {
				continue
			}
			seen[name] = true
			failures = append(failures, testFailure{Name: name, Detail: panics[m[2]]})
			continue
		}
		if m := cargoCompileFailRe.FindStringSubmatch(ln); m != nil {
			name := m[1]
			if m[2] != "" {
				name += " (" + m[2] + ")"
			}
			name += " [build failed]"
			if seen[name] {
				continue
			}
			seen[name] = true
			failures = append(failures, testFailure{Name: name, Detail: compileErr})
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

// "cucumber features/add.feature:3 # Scenario: Add two numbers" — a line from
// Cucumber's "Failing [Ss]cenarios:" summary. The default formatter prints one
// per failed scenario, as a copy-pasteable re-run command. Group 1 is the
// re-runnable "<file>.feature:<line>" location; group 2 (optional) is the
// scenario name, which the Ruby runner appends after " # " (stripping a leading
// "Scenario:"/"Scenario Outline:" keyword) but the JS runner may omit. The
// "cucumber-js" wrapper name is accepted alongside "cucumber". Requiring
// ".feature:<line>" keeps ordinary log lines that merely begin with "cucumber"
// from matching.
var cucumberFailRe = regexp.MustCompile(`^\s*cucumber(?:-js)?\s+(\S+\.feature:\d+)(?:\s+#\s+(?:Scenario(?: Outline)?:\s*)?(.+?))?\s*$`)

// parseCucumberFailures extracts failing scenarios from Cucumber output (Ruby
// `cucumber` or `cucumber-js`). Each entry in the trailing "Failing [Ss]cenarios:"
// block is a re-runnable "cucumber <file>.feature:<line> # Scenario: <name>" line;
// Name is the scenario description (falling back to the location when the runner
// omits it, as cucumber-js does) and Detail is the re-runnable location. Scenarios
// are deduplicated by name so an outline whose examples each failed at the same
// location does not produce duplicate entries.
func parseCucumberFailures(output string) []testFailure {
	var failures []testFailure
	seen := map[string]bool{}
	for _, ln := range splitLines(output) {
		m := cucumberFailRe.FindStringSubmatch(ln)
		if m == nil {
			continue
		}
		loc := strings.TrimSpace(m[1])
		name := strings.TrimSpace(m[2])
		detail := loc
		if name == "" {
			// cucumber-js omits the "# Scenario: <name>" suffix, so the location is
			// the only identifier; use it as the name and leave the detail empty.
			name = loc
			detail = ""
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		failures = append(failures, testFailure{Name: name, Detail: detail})
	}
	return failures
}

var (
	// behave closes a failing run with this exact block header, beneath which it
	// lists one indented entry per failed scenario.
	behaveFailingHeaderRe = regexp.MustCompile(`^Failing scenarios:\s*$`)
	// "  features/calc.feature:5  Add two numbers" — one entry in behave's
	// "Failing scenarios:" block. Group 1 is the re-runnable
	// "<file>.feature:<line>" location (no spaces); group 2 is the scenario name,
	// which behave separates from the location with two spaces. Requiring at least
	// two spaces as the separator keeps a feature path that itself contained a
	// single space from being split mid-location, and the scenario name (which may
	// contain single spaces) is captured whole to its trailing non-space.
	behaveScenarioRe = regexp.MustCompile(`^\s+(\S+\.feature:\d+)\s{2,}(.+\S)\s*$`)
)

// parseBehaveFailures extracts failing scenarios from behave output (`behave`,
// `python -m behave`). behave's reporter closes a failing run with a "Failing
// scenarios:" block whose indented entries are re-runnable
// "<file>.feature:<line>  <scenario name>" lines; Name is the scenario name and
// Detail the re-runnable location. Only lines inside that block are considered,
// so the verbose run's per-scenario "Scenario: <name>  # <file>:<line>" trace
// lines (printed before the summary) are not mistaken for failures. The block
// ends at the first line that is not an entry — the blank line before behave's
// "N scenarios passed, M failed" statistics. Scenarios are deduplicated by name
// so a scenario outline whose examples each failed does not produce duplicates.
func parseBehaveFailures(output string) []testFailure {
	lines := splitLines(output)
	var failures []testFailure
	seen := map[string]bool{}
	inBlock := false
	for _, ln := range lines {
		if !inBlock {
			if behaveFailingHeaderRe.MatchString(ln) {
				inBlock = true
			}
			continue
		}
		m := behaveScenarioRe.FindStringSubmatch(ln)
		if m == nil {
			// The first non-entry line (the blank separator before the statistics
			// footer) closes the block; a later second "Failing scenarios:" block,
			// if any, reopens it.
			inBlock = false
			continue
		}
		loc := strings.TrimSpace(m[1])
		name := strings.TrimSpace(m[2])
		if name == "" {
			name = loc
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		failures = append(failures, testFailure{Name: name, Detail: loc})
	}
	return failures
}

// "HeadlessChrome 120.0.0 (Linux x86_64) AppComponent should create FAILED" —
// a Karma spec-failure line. Group 1 is the browser ("<name> <version>
// (<platform>)"), captured non-greedily up to the first parenthesized platform
// group so it is stripped from the test id; group 2 is the "<suite> <test>"
// description Karma joins with spaces. The literal trailing " FAILED" anchors the
// line, keeping Karma's "Executed N of M (X FAILED)" run-summary line — whose
// "FAILED" sits inside parentheses, not at the end — from matching.
var karmaFailRe = regexp.MustCompile(`^(.+? \([^)]*\)) (.+) FAILED$`)

// parseKarmaFailures extracts failing specs from Karma's progress/dots reporter
// output (`karma start`, `ng test`). Each failure is a "<browser> <suite> <test>
// FAILED" line; Name is the "<suite> <test>" description with the browser prefix
// stripped, and Detail is the first tab/space-indented non-empty line beneath it
// (the assertion message, e.g. "Expected true to be false."), located before the
// next failure line so stack frames and the next entry are skipped. Specs are
// deduplicated by name, since a run across multiple browsers — or a reporter that
// repeats failures in its end-of-run summary — emits the same spec more than once.
func parseKarmaFailures(output string) []testFailure {
	lines := splitLines(output)
	var failures []testFailure
	seen := map[string]bool{}
	for i := 0; i < len(lines); i++ {
		m := karmaFailRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		name := strings.TrimSpace(m[2])
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		f := testFailure{Name: name}
		for j := i + 1; j < len(lines); j++ {
			if karmaFailRe.MatchString(lines[j]) {
				break
			}
			t := strings.TrimSpace(lines[j])
			if t == "" {
				continue
			}
			// The assertion message is indented (tab or spaces) beneath the header;
			// an unindented non-blank line ends the block before any message.
			if lines[j][0] != ' ' && lines[j][0] != '\t' {
				break
			}
			f.Detail = t
			break
		}
		failures = append(failures, f)
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
	// Swift Testing (the @Test framework that `swift test` runs alongside or in
	// place of XCTest on Swift 6 / Xcode 16) reports each failure through its
	// console reporter as "✘ Test <name> recorded an issue at <file>:<line>:<col>:
	// <message>" (the unicode symbol set uses "✘" U+2718; some terminals render it
	// "✗" U+2717). A test fails precisely because it recorded one or more issues,
	// so the distinct names on these lines are exactly the failing tests — keyed
	// here with their message. The suite- and run-level "✘ Test "<suite>" failed"
	// / "✘ Test run with N tests failed" lines never say "recorded an issue", so
	// matching on that phrase avoids double-counting aggregate rows. The location
	// (" at <file>:<line>:<col>:") is optional so issues without a source position
	// still surface. Group 1 is the test name, group 2 the message.
	swiftTestingIssueRe = regexp.MustCompile(`^[✘✗]\s+Test\s+(.+?)\s+recorded an issue(?: at \S+)?:?\s*(.*)$`)
)

// parseSwiftTestFailures extracts failing tests from `swift test` output,
// covering both frameworks SwiftPM can drive. XCTest marks each failure with a
// "Test Case '<id>' failed" line, its detail taken from the matching
// "<file>.swift:<line>: error: <id> : <message>" assertion line; the id format
// differs between macOS ("-[Class method]") and Linux ("Class.method") but is
// consistent within a run, so it pairs the two lines regardless of platform. The
// newer Swift Testing (@Test) framework instead prints "✘ Test <name> recorded
// an issue at <loc>: <message>" per failure, which is matched directly with its
// message as the detail. A run may emit both shapes; each failing test is
// reported once, in run order.
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
		// XCTest: "Test Case '<id>' failed".
		if m := swiftFailRe.FindStringSubmatch(ln); m != nil {
			name := strings.TrimSpace(m[1])
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			failures = append(failures, testFailure{Name: name, Detail: details[name]})
			continue
		}
		// Swift Testing: "✘ Test <name> recorded an issue at <loc>: <message>".
		// A `swift test` run can drive both frameworks at once, so both line
		// shapes are recognized in a single pass; the first issue per test keeps
		// its message and later issues for the same test are folded in.
		if m := swiftTestingIssueRe.FindStringSubmatch(ln); m != nil {
			name := strings.TrimSpace(m[1])
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			failures = append(failures, testFailure{Name: name, Detail: strings.TrimSpace(m[2])})
		}
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

var (
	// "The following tests FAILED:" — the header CTest prints before listing each
	// failed test in its run summary. Anchoring the scan on it keeps the per-test
	// "***Failed" progress lines printed earlier in the run from being mistaken for
	// entries.
	ctestHeaderRe = regexp.MustCompile(`^\s*The following tests FAILED:`)
	// "\t  2 - FailTest (Failed)" — one entry in that block. The leading number is
	// the test index, the name is the registered CTest name (re-runnable via
	// `ctest -R '^<name>$'`), and the parenthesized status (Failed, Timeout,
	// "Subprocess aborted", ...) is kept as the detail.
	ctestEntryRe = regexp.MustCompile(`^\s*\d+ - (.+) \((.+)\)\s*$`)
)

// parseCTestFailures extracts failing tests from CTest output (`ctest`, the
// CMake test driver). A failing run closes with a "The following tests FAILED:"
// block, one "<n> - <name> (<status>)" line per failure; the name is the
// registered test name and the parenthesized status (Failed, Timeout,
// "Subprocess aborted", ...) becomes the detail. The block is parsed from the
// header onward — and ends at the first non-blank, non-entry line (e.g. the
// trailing "Errors while running CTest") — so the per-test "***Failed" progress
// lines earlier in the output are never mistaken for entries.
func parseCTestFailures(output string) []testFailure {
	lines := splitLines(output)
	start := -1
	for i, ln := range lines {
		if ctestHeaderRe.MatchString(ln) {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return nil
	}
	var failures []testFailure
	seen := map[string]bool{}
	for _, ln := range lines[start:] {
		m := ctestEntryRe.FindStringSubmatch(ln)
		if m == nil {
			if strings.TrimSpace(ln) == "" {
				continue // a blank separator inside the block, not its end
			}
			break // the list is contiguous; the first other line ends it
		}
		name := strings.TrimSpace(m[1])
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		failures = append(failures, testFailure{Name: name, Detail: strings.TrimSpace(m[2])})
	}
	return failures
}

var (
	// "[  FAILED  ] MySuite.MyCase (0 ms)" — GoogleTest's per-test failure marker,
	// printed once after the failing test's body and again (without the timing
	// suffix) in the trailing "[  FAILED  ] N tests, listed below:" summary, so
	// callers deduplicate by name. Group 1 is the "<suite>.<case>" id, which
	// requires an embedded dot — that keeps the summary's own header line
	// ("[  FAILED  ] 2 tests, listed below:", whose first token is a bare number)
	// from matching. Value/type-parameterized names ("Inst/MySuite.MyCase/0") keep
	// their slashes; gtest follows them with a ", where GetParam() = ..." descriptor
	// and the "(<n> ms)" duration, both of which fall after the first space and so
	// sit outside the captured token — leaving a trailing comma the caller strips.
	gtestFailRe = regexp.MustCompile(`^\s*\[\s*FAILED\s*\]\s+(\S+\.\S+?)(?:\s+.*)?$`)
	// "[ RUN      ] MySuite.MyCase" — the line opening a test's body, used to scope
	// the search for that test's assertion detail.
	gtestRunRe = regexp.MustCompile(`^\s*\[\s*RUN\s*\]\s+(\S+\.\S+)\s*$`)
	// "[       OK ] ..." / "[  FAILED  ] ..." / "[  SKIPPED ] ..." — a test's
	// closing marker, which ends the RUN..result block so a later assertion-shaped
	// line is not misattributed to the test that just finished.
	gtestEndRe = regexp.MustCompile(`^\s*\[\s*(?:OK|FAILED|SKIPPED)\s*\]`)
	// "/path/foo_test.cc:42: Failure" / "../foo_test.cc:42: error: ..." — the
	// assertion-location header GoogleTest prints at the start of a failing check's
	// body, used as the failure's detail (file:line, like the Go parser).
	gtestDetailRe = regexp.MustCompile(`^(.+:\d+: (?:Failure|error:).*)$`)
)

// parseGTestFailures extracts failing tests from GoogleTest (gtest) console
// output. Each failure is marked "[  FAILED  ] <suite>.<case> (<t> ms)" after the
// test's body and repeated (without timing) in the trailing "[  FAILED  ] N
// tests, listed below:" summary, so failures are deduplicated by name. The detail
// is the "<file>:<line>: Failure" assertion header gtest prints inside the test's
// body, located between its "[ RUN      ]" line and its closing marker.
func parseGTestFailures(output string) []testFailure {
	lines := splitLines(output)
	// First assertion header seen inside each test's RUN..result block.
	detail := map[string]string{}
	curr := ""
	for _, ln := range lines {
		if m := gtestRunRe.FindStringSubmatch(ln); m != nil {
			curr = m[1]
			continue
		}
		if gtestEndRe.MatchString(ln) {
			curr = ""
			continue
		}
		if curr == "" {
			continue
		}
		if _, ok := detail[curr]; ok {
			continue // keep the first detail line per test
		}
		if m := gtestDetailRe.FindStringSubmatch(ln); m != nil {
			detail[curr] = strings.TrimSpace(m[1])
		}
	}
	var failures []testFailure
	seen := map[string]bool{}
	for _, ln := range lines {
		m := gtestFailRe.FindStringSubmatch(ln)
		if m == nil {
			continue
		}
		// A parameterized failure's token ends in a comma (the ", where GetParam()
		// = ..." descriptor was split off after the first space); drop it so the
		// per-test and summary lines yield the same key.
		name := strings.TrimSuffix(m[1], ",")
		if seen[name] {
			continue
		}
		seen[name] = true
		failures = append(failures, testFailure{Name: name, Detail: detail[name]})
	}
	return failures
}

var (
	// "  1) example.spec.ts:7:1 › get started link ─────────" — the numbered
	// header that opens each entry in Playwright's detailed failure section. The
	// captured group is the test title ("<file>:<line>:<col> › <name>", with an
	// optional "[project] › " prefix), which `playwright test <file>:<line>`
	// re-runs. Playwright pads the header with a run of box-drawing dashes
	// ("─", U+2500); the trailing "(?:\s*─+)?" trims them off the title.
	playwrightHeaderRe = regexp.MustCompile(`^\s*\d+\) (.+?)(?:\s*─+)?\s*$`)
	// The trailing "─"-dash decoration Playwright also appends to the failed-test
	// names in its closing "N failed" summary block, stripped so those lines (when
	// used as a fallback) match the header form.
	playwrightDashRe = regexp.MustCompile(`\s*─+\s*$`)
)

// parsePlaywrightFailures extracts failing tests from Playwright's list/line
// reporter output (`playwright test`). Each entry in the detailed failure
// section opens with a "  N) <file>:<line> › <title> ──────" header; the detail
// is the first non-empty line beneath it — the assertion or thrown-exception
// message (e.g. "Error: expect(received).toHaveTitle(expected)") — located
// before the next numbered header so an entry without a body does not borrow the
// following one's. The trailing box-drawing dashes Playwright pads the header
// with are trimmed from the name.
func parsePlaywrightFailures(output string) []testFailure {
	lines := splitLines(output)
	var failures []testFailure
	seen := map[string]bool{}
	for i := 0; i < len(lines); i++ {
		m := playwrightHeaderRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		name := strings.TrimSpace(playwrightDashRe.ReplaceAllString(m[1], ""))
		// A genuine Playwright failure header names a test location ("file › title");
		// a bare "1) note" line elsewhere in the output lacks the " › " separator and
		// is skipped so it is not mistaken for a failing test.
		if name == "" || !strings.Contains(name, "›") || seen[name] {
			continue
		}
		seen[name] = true
		f := testFailure{Name: name}
		for j := i + 1; j < len(lines); j++ {
			if playwrightHeaderRe.MatchString(lines[j]) {
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
	// "  1) Failure:" / "  1) Error:" — the numbered header that opens each entry
	// in Minitest's failure report. Minitest numbers failures and errors together
	// in a single sequence, and the kind ("Failure"/"Error") is captured so it can
	// distinguish an assertion failure from an uncaught exception.
	minitestHeaderRe = regexp.MustCompile(`^\s*\d+\) (Failure|Error):\s*$`)
	// "CalculatorTest#test_addition [test/calculator_test.rb:8]:" — the test id
	// line printed immediately beneath a header. The id is "Class#method" (the
	// nesting "::" of a namespaced class is part of Class), optionally followed by
	// a " [file:line]" source location; both end in a colon. The id is the
	// re-runnable name; the bracketed location is captured as a detail fallback.
	minitestIDRe = regexp.MustCompile(`^(\S+#\S+?)(?: \[([^\]]+)\])?:\s*$`)
)

// parseMinitestFailures extracts failing tests from Minitest output (`rails
// test`/`rake test`/`ruby -Itest ...`). Each entry opens with a "  N) Failure:"
// or "  N) Error:" header, followed by a "Class#method [file:line]:" id line and
// then the assertion or exception message. Name is the "Class#method" id; Detail
// is the first non-empty message line after the id (e.g. "Expected: 5" or
// "ZeroDivisionError: divided by 0"), located before the next header so an entry
// without a body does not borrow the following one's. When no message line is
// present the bracketed source location is used as the detail instead.
func parseMinitestFailures(output string) []testFailure {
	lines := splitLines(output)
	var failures []testFailure
	seen := map[string]bool{}
	for i := 0; i < len(lines); i++ {
		if minitestHeaderRe.FindStringSubmatch(lines[i]) == nil {
			continue
		}
		// The id sits on the next non-empty line beneath the header.
		j := i + 1
		for j < len(lines) && strings.TrimSpace(lines[j]) == "" {
			j++
		}
		if j >= len(lines) {
			break
		}
		idm := minitestIDRe.FindStringSubmatch(strings.TrimSpace(lines[j]))
		if idm == nil {
			continue
		}
		name := strings.TrimSpace(idm[1])
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		f := testFailure{Name: name}
		// The assertion/exception message is the first non-empty line after the id,
		// before the next numbered header. Fall back to the bracketed location.
		for k := j + 1; k < len(lines); k++ {
			if minitestHeaderRe.MatchString(lines[k]) {
				break
			}
			if t := strings.TrimSpace(lines[k]); t != "" {
				f.Detail = t
				break
			}
		}
		if f.Detail == "" {
			f.Detail = idm[2]
		}
		failures = append(failures, f)
	}
	return failures
}

var (
	// "00:01 +0 -1: my group my test [E]" — the compact-reporter status line Dart's
	// package:test runner prints for a failing test (`dart test`/`flutter test`).
	// The "MM:SS" elapsed clock is followed by the running "+passed", optional
	// "~skipped", and "-failed" counters, then the test name (group labels folded
	// in, space-separated) and a trailing " [E]" marker that distinguishes a
	// failure from a passing update. The closing "Some tests failed." summary line
	// carries the counter prefix but no " [E]", so it does not match.
	dartFailRe = regexp.MustCompile(`^\d{2}:\d{2} \+\d+(?: ~\d+)?(?: -\d+)?: (.+) \[E\]\s*$`)
	// A new compact-reporter status line, used to bound a failure's indented detail
	// block so it does not bleed into the next test's output.
	dartStatusRe = regexp.MustCompile(`^\d{2}:\d{2} `)
)

// parseDartTestFailures extracts failing tests from Dart package:test output
// (`dart test`/`flutter test`). Each failing test prints a
// "MM:SS +P -F: <name> [E]" status line; the name (with any group labels folded
// in) is the failing test, reported in run order and deduplicated since a test
// with multiple errors emits the line more than once. The detail is the first
// non-empty indented line of the assertion/exception block beneath it (e.g.
// "Expected: <2>" or "TestFailure: ..."), located before the next status line so
// an entry without a body does not borrow the following one's.
func parseDartTestFailures(output string) []testFailure {
	lines := splitLines(output)
	var failures []testFailure
	seen := map[string]bool{}
	for i := 0; i < len(lines); i++ {
		m := dartFailRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		name := strings.TrimSpace(m[1])
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		f := testFailure{Name: name}
		// The assertion/exception message sits on the first non-empty indented line
		// beneath the status line, before the next status line.
		for j := i + 1; j < len(lines); j++ {
			if dartStatusRe.MatchString(lines[j]) {
				break
			}
			if lines[j] == "" || (lines[j][0] != ' ' && lines[j][0] != '\t') {
				continue
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
	// "Test Failed at /path/runtests.jl:7" / "Error During Test at /path:12" — the
	// header opening a Julia Test.jl failure block. @test assertions carry no
	// names, so the source location identifies the failure. An optional
	// "<testset>: " label may precede it; the matcher keys on the header itself so
	// the label is ignored.
	juliaFailRe = regexp.MustCompile(`(?:Test Failed|Error During Test) at (\S+:\d+)`)
	// "  Expression: add(2, 2) == 5" — the tested expression, printed on the first
	// indented line of the block. It is the most useful one-line detail.
	juliaExprRe = regexp.MustCompile(`^\s+Expression:\s*(.*\S)\s*$`)
)

// parseJuliaTestFailures extracts failing tests from Julia's stdlib Test.jl
// output (`julia test/runtests.jl`, `julia -e 'using Pkg; Pkg.test()'`). Each
// failure block opens with a "Test Failed at <file>:<line>" or "Error During
// Test at <file>:<line>" header; since @test assertions carry no names, the
// source location names the failure. The detail is the block's "Expression:"
// line — the actual assertion — located before the next header. Failures are
// returned in run order and deduplicated by location.
func parseJuliaTestFailures(output string) []testFailure {
	lines := splitLines(output)
	var failures []testFailure
	seen := map[string]bool{}
	for i := 0; i < len(lines); i++ {
		m := juliaFailRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		name := strings.TrimSpace(m[1])
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		f := testFailure{Name: name}
		for j := i + 1; j < len(lines); j++ {
			if juliaFailRe.MatchString(lines[j]) {
				break
			}
			if d := juliaExprRe.FindStringSubmatch(lines[j]); d != nil {
				f.Detail = strings.TrimSpace(d[1])
				break
			}
		}
		failures = append(failures, f)
	}
	return failures
}

var (
	// "[info] " / "[error] " — the per-line log-level prefix sbt prepends to test
	// reporter output. Stripping it first lets the failure and detail matchers key
	// on the reporter's own text regardless of which stream sbt routed the line to.
	sbtLogPrefixRe = regexp.MustCompile(`^\[(?:info|error|warn)\]\s?`)
	// "- should add two numbers *** FAILED ***" — a failing example as ScalaTest's
	// FlatSpec/FunSuite/WordSpec/FreeSpec reporters print it (after the sbt prefix
	// is removed). The leaf name sits between the "- " bullet and the
	// " *** FAILED ***" marker; the run summary "*** 1 TEST FAILED ***" has no "- "
	// bullet, so it does not match.
	scalaFailRe = regexp.MustCompile(`^- (.+?) \*\*\* FAILED \*\*\*\s*$`)
)

// parseScalaTestFailures extracts failing tests from sbt + ScalaTest output
// (`sbt test`/`sbt testOnly`). The sbt "[info]"/"[error]" log prefix is stripped
// from each line first; a failure then appears as "- <name> *** FAILED ***", and
// the detail is the first non-empty line beneath it — the assertion message and
// source location, e.g. "2 did not equal 3 (CalculatorSpec.scala:15)". Scanning
// stops at the next example (any "- " line, failing or passing) so an entry
// without a body does not borrow the following one's.
func parseScalaTestFailures(output string) []testFailure {
	lines := splitLines(output)
	var failures []testFailure
	seen := map[string]bool{}
	for i := 0; i < len(lines); i++ {
		head := sbtLogPrefixRe.ReplaceAllString(lines[i], "")
		m := scalaFailRe.FindStringSubmatch(head)
		if m == nil {
			continue
		}
		name := strings.TrimSpace(m[1])
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		f := testFailure{Name: name}
		for j := i + 1; j < len(lines); j++ {
			body := sbtLogPrefixRe.ReplaceAllString(lines[j], "")
			// The next example (a "- " bullet, passing or failing) ends this block.
			if strings.HasPrefix(strings.TrimSpace(body), "- ") {
				break
			}
			if t := strings.TrimSpace(body); t != "" {
				f.Detail = t
				break
			}
		}
		failures = append(failures, f)
	}
	return failures
}

var (
	// "FAIL in (a-test) (core_test.clj:7)" / "ERROR in (a-test) (core_test.clj:9)"
	// — the header clojure.test's default reporter prints for each failing or
	// erroring assertion. Group 1 is the test var, group 2 the "file:line" site.
	// lein/Cognitect's test-runner relay the same header verbatim, so one matcher
	// covers them. The line may carry leading whitespace under nested reporters.
	clojureFailRe = regexp.MustCompile(`^\s*(?:FAIL|ERROR) in \((.+?)\) \((.+?)\)`)
	// "  actual: (not (= 0 1))" — the indented line clojure.test prints with the
	// failing form (or the thrown exception, for an ERROR). It is the closest
	// thing to a one-line assertion message, so it is used as the detail.
	clojureActualRe = regexp.MustCompile(`^\s*actual:\s*(.+)$`)
)

// parseClojureTestFailures extracts failures from clojure.test output
// (`lein test`/`clojure -M:test`/`clj -X:test`/`kaocha`). Each failing or
// erroring assertion opens with a "FAIL in (<var>) (<file>:<line>)" /
// "ERROR in (<var>) (<file>:<line>)" header; Name is "<var> (<file>:<line>)" so
// the failing form's source site travels with the test name, and Detail is the
// first following indented "actual:" line — the failing form or thrown exception.
// Scanning for the detail stops at the next header so an entry without an
// "actual:" line does not borrow the following failure's.
func parseClojureTestFailures(output string) []testFailure {
	lines := splitLines(output)
	var failures []testFailure
	seen := map[string]bool{}
	for i := 0; i < len(lines); i++ {
		m := clojureFailRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		name := strings.TrimSpace(m[1]) + " (" + strings.TrimSpace(m[2]) + ")"
		if seen[name] {
			continue
		}
		seen[name] = true
		f := testFailure{Name: name}
		for j := i + 1; j < len(lines); j++ {
			if clojureFailRe.MatchString(lines[j]) {
				break
			}
			if d := clojureActualRe.FindStringSubmatch(lines[j]); d != nil {
				f.Detail = strings.TrimSpace(d[1])
				break
			}
		}
		failures = append(failures, f)
	}
	return failures
}

// "[FAIL: assertion failed] test_Increment() (gas: 28379)" — the line Foundry's
// `forge test` reporter prints for each failing test. The optional reason follows
// the marker as either "[FAIL: <reason>]" (current) or "[FAIL. Reason: <reason>]"
// (older builds); group 1 is that reason (empty for a bare "[FAIL]"), and group 2
// is the Solidity test function with its parameter list — e.g. "test_Increment()"
// or "testFuzz_SetNumber(uint256)" — which is how a test is re-run via
// `forge test --match-test`. The trailing " (gas: ...)"/" (runs: ...)" stats are
// not captured.
var foundryFailRe = regexp.MustCompile(`^\s*\[FAIL(?:(?:: ?|\. Reason: ?)(.*?))?\]\s+(\w+\([^)]*\))`)

// parseFoundryTestFailures extracts failing tests from Foundry output
// (`forge test`). Each failure is a "[FAIL: <reason>] <fn>(<args>) (gas: N)" line
// (older builds write "[FAIL. Reason: <reason>]"); Name is the Solidity test
// function with its parameter list and Detail is the revert/assertion reason when
// the marker carries one. Failures are returned in run order and deduplicated by
// name so a fuzz test reported once per shrink does not produce duplicates.
func parseFoundryTestFailures(output string) []testFailure {
	lines := splitLines(output)
	var failures []testFailure
	seen := map[string]bool{}
	for _, ln := range lines {
		m := foundryFailRe.FindStringSubmatch(ln)
		if m == nil {
			continue
		}
		name := strings.TrimSpace(m[2])
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		failures = append(failures, testFailure{Name: name, Detail: strings.TrimSpace(m[1])})
	}
	return failures
}

var (
	// "Summarizing 2 Failures:" — the header Ginkgo's reporter prints before the
	// final block that lists every failed spec. The parser keys on this block
	// rather than the inline "• [FAILED]" markers because the summary names each
	// spec in full and is emitted once per spec regardless of retries.
	ginkgoSummaryRe = regexp.MustCompile(`^\s*Summarizing \d+ Failure`)
	// "  [FAIL] Book Categorizing when short [It] should be a short book" — one
	// entry in the summary block. The marker is the outcome (FAIL, PANIC!,
	// TIMEOUT, SPEC TIMEOUT, INTERRUPTED, ABORTED); group 1 is the full,
	// space-joined spec text, which is what `ginkgo --focus` re-runs. The inline
	// per-spec output spells its marker "[FAILED]" (with a D), so anchoring to the
	// summary block keeps the two from colliding.
	ginkgoFailRe = regexp.MustCompile(`^\s*\[(?:FAIL|PANIC!|TIMEOUT|SPEC TIMEOUT|INTERRUPTED|ABORTED)\]\s+(.+\S)\s*$`)
	// "  /path/to/book_test.go:54" — the source location Ginkgo prints on the line
	// beneath a summary entry, used as the failure's detail.
	ginkgoLocRe = regexp.MustCompile(`^\s*(\S+\.go:\d+)\s*$`)
)

// parseGinkgoFailures extracts failing specs from Ginkgo (the Go BDD runner)
// output. It scans the trailing "Summarizing N Failures:" block: each
// "[FAIL]/[PANIC!]/[TIMEOUT] <spec>" entry names a failed spec (Name is the full
// hierarchical spec text), and the "<file>.go:<line>" location printed on the
// next line becomes its detail. Specs are returned in report order and
// deduplicated by name. Returns nil when no summary block is present.
func parseGinkgoFailures(output string) []testFailure {
	lines := splitLines(output)
	start := -1
	for i, ln := range lines {
		if ginkgoSummaryRe.MatchString(ln) {
			start = i + 1
		}
	}
	if start < 0 {
		return nil
	}
	var failures []testFailure
	seen := map[string]bool{}
	for i := start; i < len(lines); i++ {
		m := ginkgoFailRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		name := strings.TrimSpace(m[1])
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		f := testFailure{Name: name}
		if i+1 < len(lines) {
			if loc := ginkgoLocRe.FindStringSubmatch(lines[i+1]); loc != nil {
				f.Detail = strings.TrimSpace(loc[1])
			}
		}
		failures = append(failures, f)
	}
	return failures
}

var (
	// "Failures:" — the header Jasmine's ConsoleReporter prints before listing each
	// failed spec. Anchoring the scan on it keeps the inline progress output (the
	// "F"/"." dots and any "Pending:" section) from being mistaken for entries.
	jasmineFailuresRe = regexp.MustCompile(`^Failures:\s*$`)
	// "1) A calculator adds numbers" — the numbered header of an entry in the
	// "Failures:" block. The captured group is the full spec name (suite
	// descriptions joined with the spec description), which is what Jasmine prints.
	jasmineHeaderRe = regexp.MustCompile(`^\s*\d+\) (.+)$`)
	// "  Message:" — the label Jasmine prints immediately above a failure's
	// assertion message. The message itself is the first non-empty line beneath it.
	jasmineMessageRe = regexp.MustCompile(`^\s*Message:\s*$`)
	// "1 spec, 1 failure" / "3 specs, 2 failures, 1 pending spec" — the run-summary
	// line Jasmine prints after the failure block. It ends the scan so a trailing
	// numbered list outside the block is never mistaken for more entries.
	jasmineSummaryRe = regexp.MustCompile(`^\d+ specs?, \d+ failures?\b`)
)

// parseJasmineFailures extracts failing specs from Jasmine's default
// ConsoleReporter output (`jasmine`/`npx jasmine`). A failing run closes with a
// "Failures:" block, each entry opening with a "N) <full spec name>" header
// followed by an indented "Message:" line whose first non-empty body line is the
// assertion. The block is parsed from the "Failures:" header onward and ends at
// the "N specs, N failures" run summary (or a following "Pending:" section), so
// the inline progress dots and pending specs are never mistaken for failures.
func parseJasmineFailures(output string) []testFailure {
	lines := splitLines(output)
	start := -1
	for i, ln := range lines {
		if jasmineFailuresRe.MatchString(ln) {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return nil
	}
	var failures []testFailure
	seen := map[string]bool{}
	for i := start; i < len(lines); i++ {
		// The block ends at the run summary or a following section header (e.g.
		// "Pending:"); stop so entries from outside it are not collected.
		if jasmineSummaryRe.MatchString(lines[i]) || strings.TrimSpace(lines[i]) == "Pending:" {
			break
		}
		m := jasmineHeaderRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		name := strings.TrimSpace(m[1])
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		f := testFailure{Name: name}
		// The assertion sits on the first non-empty line after the "Message:" label,
		// before the next numbered header so an entry without a message body does not
		// borrow the following one's.
		for j := i + 1; j < len(lines); j++ {
			if jasmineHeaderRe.MatchString(lines[j]) {
				break
			}
			if !jasmineMessageRe.MatchString(lines[j]) {
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
	// "  [-] Returns Mars 8ms (7ms|1ms)" — Pester's per-test failure line in its
	// detailed console output. "[+]" marks a passing test and "[!]" a skipped one,
	// so only "[-]" is a failure. The captured group is everything after the marker
	// up to the trailing duration, which pesterTimeRe strips.
	pesterFailRe = regexp.MustCompile(`^\s*\[-\]\s+(\S.*?)\s*$`)
	// The trailing " 8ms (7ms|1ms)" / " 1.2s" / " 523ms" duration marker on a
	// Pester result line. The optional "(user|framework)" breakdown follows the
	// total when Pester is configured to show it.
	pesterTimeRe = regexp.MustCompile(`\s+\d+(?:\.\d+)?\s*m?s(?:\s+\([^)]*\))?$`)
	// Any Pester result marker ("[+]" pass, "[-]" fail, "[!]" skip, "[?]"
	// inconclusive). Used to bound a failure's detail scan so it stops at the next
	// test's line rather than borrowing it.
	pesterMarkerRe = regexp.MustCompile(`^\s*\[[-+!?]\]`)
)

// parsePesterFailures extracts failing tests from Pester's detailed console
// output (`Invoke-Pester`). Each failure opens with an indented "[-] <name>
// <duration>" line; the detail is the first non-empty line beneath it (the
// assertion message, e.g. "Expected 'Mars', but got 'Earth'."), located before
// the next result marker or "Describing"/"Context" block header so an entry
// without a message body does not borrow the following one's. The trailing
// "at <expr>, <file>:<line>" location line is skipped in favor of the message
// above it.
func parsePesterFailures(output string) []testFailure {
	lines := splitLines(output)
	var failures []testFailure
	seen := map[string]bool{}
	for i := 0; i < len(lines); i++ {
		m := pesterFailRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		name := strings.TrimSpace(pesterTimeRe.ReplaceAllString(m[1], ""))
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		f := testFailure{Name: name}
		for j := i + 1; j < len(lines); j++ {
			if pesterMarkerRe.MatchString(lines[j]) {
				break
			}
			t := strings.TrimSpace(lines[j])
			if strings.HasPrefix(t, "Describing ") || strings.HasPrefix(t, "Context ") {
				break
			}
			if t != "" {
				f.Detail = t
				break
			}
		}
		failures = append(failures, f)
	}
	return failures
}

var (
	// "Addition Works                              | FAIL |" — Robot Framework's
	// per-test result row: the test name, left-justified and padded, ahead of the
	// "| FAIL |" status. Each suite closes with a row of the same shape, told
	// apart by robotStatsRe (see parseRobotFailures).
	robotFailRowRe = regexp.MustCompile(`^(\S.*?)\s+\| FAIL \|\s*$`)
	// "2 tests, 1 passed, 1 failed" — the statistics line Robot prints beneath a
	// suite's "| FAIL |" row, never beneath a test's. It marks the row above as a
	// suite summary rather than a failing test. A leading "N critical tests, ..."
	// variant (older Robot versions) is matched too.
	robotStatsRe = regexp.MustCompile(`^\d+ (?:critical )?tests?, \d+ passed`)
	// A Robot section separator ("===" between suites, "---" between tests), which
	// follows a failing test that carried no message so it is not taken as detail.
	robotSepRe = regexp.MustCompile(`^[=-]{10,}$`)
)

// parseRobotFailures extracts failing tests from Robot Framework console output.
// Each test renders as a "<name> ... | FAIL |" row and the line beneath carries
// the failure message, which becomes the detail. Robot closes every suite with a
// row of the same shape, but a suite row is followed by a "N tests, M passed, K
// failed" statistics line — detected and skipped — so only genuine tests are
// reported. Names are deduplicated.
func parseRobotFailures(output string) []testFailure {
	lines := splitLines(output)
	var failures []testFailure
	seen := map[string]bool{}
	for i := 0; i < len(lines); i++ {
		m := robotFailRowRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		// The first non-empty line beneath the row decides what it is: a statistics
		// line marks a suite summary (skip it); a separator marks a message-less
		// failure; anything else is the failing test's message.
		detail, isSuite := "", false
		for j := i + 1; j < len(lines); j++ {
			t := strings.TrimSpace(lines[j])
			if t == "" {
				continue
			}
			switch {
			case robotStatsRe.MatchString(t):
				isSuite = true
			case !robotSepRe.MatchString(t):
				detail = t
			}
			break
		}
		if isSuite {
			continue
		}
		name := strings.TrimSpace(m[1])
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		failures = append(failures, testFailure{Name: name, Detail: detail})
	}
	return failures
}

var (
	// "Failure → spec/example_spec.lua @ 4" — Busted's per-failure block header.
	// "Error → ..." marks an uncaught Lua error (rather than a failed assertion);
	// both are actionable failures. The plainTerminal handler renders the arrow as
	// "->", so the separator between the keyword and "<file> @ <line>" is matched
	// loosely. Anchoring on the trailing "@ <line>" keeps the following
	// description and "<file>:<line>:" detail lines from matching the header.
	bustedHeaderRe = regexp.MustCompile(`^(?:Failure|Error)\b.*@ \d+$`)
	// "spec/example_spec.lua:5: Expected objects to be equal." — the assertion or
	// error detail Busted prints beneath the test description. The leading
	// "<file>:<line>: " locates it; the description line above never matches.
	bustedDetailRe = regexp.MustCompile(`^\S+:\d+: .+`)
)

// parseBustedFailures extracts failing examples from Busted (Lua) output. Each
// failure renders as a "Failure → <file> @ <line>" (or "Error → ...") header,
// the full test description on the line beneath — used as the name so it reads
// like the other runners' human-readable identifiers — and a
// "<file>:<line>: <message>" assertion line as the detail when present. Blocks
// are bounded by the next header so one failure's trailing output is not read as
// the next one's detail.
func parseBustedFailures(output string) []testFailure {
	lines := splitLines(output)
	var failures []testFailure
	for i := 0; i < len(lines); i++ {
		if !bustedHeaderRe.MatchString(strings.TrimSpace(lines[i])) {
			continue
		}
		// The test description is the first non-empty line beneath the header.
		j := i + 1
		for j < len(lines) && strings.TrimSpace(lines[j]) == "" {
			j++
		}
		if j >= len(lines) {
			break
		}
		f := testFailure{Name: strings.TrimSpace(lines[j])}
		// The detail is the first "<file>:<line>: <message>" line after the
		// description, before the next failure block begins.
		for k := j + 1; k < len(lines); k++ {
			t := strings.TrimSpace(lines[k])
			if bustedHeaderRe.MatchString(t) {
				break
			}
			if bustedDetailRe.MatchString(t) {
				f.Detail = t
				break
			}
		}
		if f.Name != "" {
			failures = append(failures, f)
		}
		i = j
	}
	return failures
}

var (
	// hspec closes a failing run with a "Failures:" section listing each failed
	// example as "N) <full description>". Group 1 is the description, used as the
	// name so it reads like the other runners' human-readable identifiers. The
	// leading "\s*\d+\)" matches hspec's indented "  1) ..." numbering.
	haskellFailRe = regexp.MustCompile(`^\s*\d+\) (.+\S)\s*$`)
	// The indented source-location line hspec prints directly above each numbered
	// failure, e.g. "  test/FooSpec.hs:14:7: " (a trailing space, or nothing,
	// follows the final colon). Group 1 is the "<file>:<line>:<col>" coordinate,
	// surfaced as the failure detail so the model can jump straight to the
	// assertion. The "$" anchor (after an optional colon and trailing spaces)
	// keeps message lines that merely mention a path from matching. ".l?hs"
	// admits both ordinary (.hs) and literate (.lhs) Haskell sources.
	haskellLocRe = regexp.MustCompile(`^\s*(\S+\.l?hs:\d+:\d+):?\s*$`)
)

// parseHaskellFailures extracts failing examples from a Haskell test run
// (hspec, the dominant framework, driven by `stack test`/`cabal test`). hspec
// closes a failing run with a "Failures:" section that numbers each failed
// example ("N) <description>") and prints its "<file>.hs:<line>:<col>:" source
// location on the indented line directly above. The description becomes the
// name and that location the detail. Parsing is gated on the "Failures:" header
// so ordinary numbered output earlier in the log is never misread as a failure,
// and a run that drives a different framework (tasty), which prints no such
// header, simply yields nothing rather than a false positive.
func parseHaskellFailures(output string) []testFailure {
	lines := splitLines(output)
	inFailures := false
	// The most recent source-location line seen; it precedes the numbered
	// failure it belongs to, and is cleared once consumed so a failure without
	// its own location does not borrow the previous one's.
	lastLoc := ""
	var failures []testFailure
	seen := map[string]bool{}
	for _, ln := range lines {
		if !inFailures {
			if strings.TrimSpace(ln) == "Failures:" {
				inFailures = true
			}
			continue
		}
		if m := haskellLocRe.FindStringSubmatch(ln); m != nil {
			lastLoc = m[1]
			continue
		}
		if m := haskellFailRe.FindStringSubmatch(ln); m != nil {
			name := strings.TrimSpace(m[1])
			if name != "" && !seen[name] {
				seen[name] = true
				failures = append(failures, testFailure{Name: name, Detail: lastLoc})
			}
			lastLoc = ""
		}
	}
	return failures
}

var (
	// "//pkg:target                         FAILED in 0.3s" — Bazel's per-target
	// result row in the run summary. The target is right-padded with spaces to a
	// fixed column before the status, so any run of whitespace separates them.
	// The "in ..." tail varies: a plain "in 0.3s", a flaky "in 2 out of 3 in
	// 0.5s", or (rarely) just "FAILED" with no timing, so everything after the
	// status word is ignored. Group 1 is the "//pkg:target" label.
	bazelFailRe = regexp.MustCompile(`^(//\S+)\s+FAILED(?:\s.*)?$`)
	// "  /home/user/.cache/bazel/.../testlogs/pkg/target/test.log" — the indented
	// path to a failed target's log, printed on the line beneath its FAILED row.
	// Used as the failure's detail so the model can open the captured output.
	bazelLogRe = regexp.MustCompile(`^\s+(\S+\.log)\s*$`)
)

// parseBazelFailures extracts failing targets from `bazel test` output. Bazel
// wraps each language's own runner but reliably prints a target-level summary
// row per test ("//pkg:target    FAILED in <time>"), so the target is the unit
// surfaced here. The detail is the indented test.log path Bazel prints beneath a
// failed row, when present, so the model can open the captured output for the
// underlying assertion. A target's row can appear more than once (e.g. a flaky
// retry, or repeated across streamed and summary sections), so failures are
// deduplicated by target in first-seen order.
func parseBazelFailures(output string) []testFailure {
	lines := splitLines(output)
	var failures []testFailure
	seen := map[string]bool{}
	for i := 0; i < len(lines); i++ {
		m := bazelFailRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		target := m[1]
		if seen[target] {
			continue
		}
		seen[target] = true
		f := testFailure{Name: target}
		// The log path, when Bazel printed one, sits on the immediately following
		// indented line.
		if i+1 < len(lines) {
			if d := bazelLogRe.FindStringSubmatch(lines[i+1]); d != nil {
				f.Detail = strings.TrimSpace(d[1])
			}
		}
		failures = append(failures, f)
	}
	return failures
}

var (
	// "crystal spec spec/foo_spec.cr:5 # Foo does something" — a line from the
	// "Failed examples:" block Crystal's default spec formatter prints at the end
	// of a failing run, one per failure, as a copy-pasteable re-run command. Group
	// 1 is the re-runnable "<file>.cr:<line>" location; group 2 is the example's
	// description. Requiring ".cr:<line>" keeps ordinary log lines that merely
	// begin with "crystal spec" from matching.
	crystalFailedExampleRe = regexp.MustCompile(`^\s*crystal spec (\S+\.cr:\d+) # (.+)$`)
	// "  1) Foo does something" — the numbered header of an entry in Crystal's
	// "Failures:" block, used as a fallback (and to recover the assertion detail)
	// when the "Failed examples:" summary is absent.
	crystalFailureHeaderRe = regexp.MustCompile(`^\s*\d+\) (.+)$`)
	// "       Expected: 4" / "       Unhandled exception: ..." — the first
	// indented body line beneath a numbered failure header, the closest thing
	// Crystal prints to a one-line assertion/error message.
	crystalFailureDetailRe = regexp.MustCompile(`^\s+(Expected:.*|Unhandled exception:.*)$`)
)

// parseCrystalFailures extracts failures from Crystal spec output (`crystal
// spec`). The "Failed examples:" summary gives a clean, single-line "crystal
// spec <location> # <description>" per failure (Name is the description, Detail
// the re-runnable location), mirroring RSpec's format. When that summary is
// absent — e.g. a suite that errored before printing it — it falls back to the
// numbered "Failures:" block, pairing each "N) <description>" header with its
// following "Expected:"/"Unhandled exception:" line as the detail. Failures are
// deduplicated by name.
func parseCrystalFailures(output string) []testFailure {
	lines := splitLines(output)
	var failures []testFailure
	seen := map[string]bool{}
	for _, ln := range lines {
		if m := crystalFailedExampleRe.FindStringSubmatch(ln); m != nil {
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
		m := crystalFailureHeaderRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		f := testFailure{Name: strings.TrimSpace(m[1])}
		for j := i + 1; j < len(lines) && j <= i+6; j++ {
			if e := crystalFailureDetailRe.FindStringSubmatch(lines[j]); e != nil {
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

var (
	// "Test [N/M] test "name"... FAIL (reason)" — Zig's test runner marks each
	// failed test with this line. Group 1 is the test description (the string
	// literal passed to the `test` block), group 2 (optional) is the failure
	// reason in parens, e.g. "AssertionError at src/main.zig:5:5" or "panicked".
	// The FAIL is followed by optional whitespace and parens so "... FAIL\n" (no
	// reason, some older builds) and "... FAIL (reason)" both match.
	zigTestFailRe = regexp.MustCompile(`^Test \[\d+/\d+\] test "([^"]+)"\.\.\. FAIL(?:\s*\(([^)]+)\))?$`)
)

// parseZigTestFailures extracts failures from Zig's built-in test runner output
// (`zig test` or `zig build test`). Each failing test is reported on a line:
//
//	Test [N/M] test "name"... FAIL (reason)
//
// Name is the quoted test description; Detail is the reason in parentheses (e.g.
// "AssertionError at src/main.zig:5:5") when the runner supplies it. Tests are
// deduplicated by name in case the same failure appears in both inline output and
// a summary block.
func parseZigTestFailures(output string) []testFailure {
	var failures []testFailure
	seen := map[string]bool{}
	for _, ln := range splitLines(output) {
		m := zigTestFailRe.FindStringSubmatch(ln)
		if m == nil {
			continue
		}
		name := m[1]
		if seen[name] {
			continue
		}
		seen[name] = true
		failures = append(failures, testFailure{Name: name, Detail: strings.TrimSpace(m[2])})
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

// maxFailureDetailWidth bounds the length of a single failure's detail line in
// the inline summary. A detail is one line of runner output, but that line can
// be arbitrarily long — an assertion that prints a large expected-vs-actual diff
// or a deeply nested value renders as a single multi-hundred-character string.
// Rendering it verbatim per failure floods the bash result and the model's
// context. The untruncated detail is always preserved in
// Metadata[MetadataTestFailures], so nothing is lost — only the inline render is
// clipped, with a trailing ellipsis so the truncation is explicit.
const maxFailureDetailWidth = 200

// summarizeTestFailures renders a compact, agent-friendly block listing the
// failed tests (and their detail line when known). At most maxSummarizedFailures
// entries are shown; any beyond that are collapsed into a "... and N more" line.
// Each detail is clipped to maxFailureDetailWidth runes. Returns "" for no
// failures.
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
			fmt.Fprintf(&b, "\n  %s — %s", f.Name, truncateDetail(f.Detail))
		} else {
			fmt.Fprintf(&b, "\n  %s", f.Name)
		}
	}
	if remaining := len(failures) - len(shown); remaining > 0 {
		fmt.Fprintf(&b, "\n  ... and %d more", remaining)
	}
	return b.String()
}

// truncateDetail clips detail to maxFailureDetailWidth runes, appending a
// single-character ellipsis when it had to cut. The cut lands on a rune boundary
// so multi-byte content is never split mid-character.
func truncateDetail(detail string) string {
	if utf8.RuneCountInString(detail) <= maxFailureDetailWidth {
		return detail
	}
	runes := []rune(detail)
	return string(runes[:maxFailureDetailWidth]) + "…"
}
