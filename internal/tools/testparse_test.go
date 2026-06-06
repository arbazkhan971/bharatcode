package tools

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestClassifyTestRunner(t *testing.T) {
	cases := map[string]testRunner{
		"go test ./...":                              runnerGo,
		"GOFLAGS=-count=1 go test -run X ./p":        runnerGo,
		"pytest -q tests/":                           runnerPytest,
		"python -m py.test":                          runnerPytest,
		"python -m unittest test_mod":                runnerUnittest,
		"python -m unittest discover":                runnerUnittest,
		"nose2":                                      runnerUnittest,
		"nose2 -v tests":                             runnerUnittest,
		"python -m nose2":                            runnerUnittest,
		"nosetests tests/":                           runnerUnittest,
		"echo a diagnosis2 of the problem":           runnerNone,
		"tox":                                        runnerTox,
		"tox -e py311":                               runnerTox,
		"uvx tox":                                    runnerTox,
		"nox":                                        runnerTox,
		"nox -s tests":                               runnerTox,
		"echo feeling intoxicated":                   runnerNone,
		"echo an obnoxious bug":                      runnerNone,
		"tox -e py -- pytest -k foo":                 runnerPytest,
		"npm test":                                   runnerJest,
		"npm run test -- --ci":                       runnerJest,
		"yarn test":                                  runnerJest,
		"yarn run test":                              runnerJest,
		"yarn run test --ci":                         runnerJest,
		"pnpm test":                                  runnerJest,
		"pnpm run test":                              runnerJest,
		"pnpm run test -- --coverage":                runnerJest,
		"npx vitest run":                             runnerJest,
		"npx jest src/":                              runnerJest,
		"cargo test":                                 runnerCargo,
		"cargo test --release foo":                   runnerCargo,
		"cargo nextest run":                          runnerNextest,
		"cargo nextest run --no-fail-fast":           runnerNextest,
		"rspec":                                      runnerRSpec,
		"bundle exec rspec spec/foo_spec.rb":         runnerRSpec,
		"bin/rspec":                                  runnerRSpec,
		"vendor/bin/phpunit":                         runnerPHPUnit,
		"phpunit --filter testFoo":                   runnerPHPUnit,
		"dotnet test":                                runnerDotnet,
		"dotnet test ./MyApp.sln -v normal":          runnerDotnet,
		"mvn test":                                   runnerMaven,
		"mvn -q verify -Dtest=FooTest":               runnerMaven,
		"./mvnw test":                                runnerMaven,
		"gradle test":                                runnerGradle,
		"./gradlew test --tests FooTest":             runnerGradle,
		"gradlew check":                              runnerGradle,
		"mix test":                                   runnerExUnit,
		"mix test test/foo_test.exs:12":              runnerExUnit,
		"node --test":                                runnerTAP,
		"node --test test/*.js":                      runnerTAP,
		"npx tape test/*.js":                         runnerTAP,
		"bats test/":                                 runnerTAP,
		"bats test.bats":                             runnerTAP,
		"npx bats tests/":                            runnerTAP,
		"prove -lv t/":                               runnerTAP,
		"perl Build.PL && prove":                     runnerTAP,
		"echo the change was approved":               runnerNone,
		"echo acrobats perform":                      runnerNone,
		"deno test":                                  runnerDeno,
		"deno test --allow-read mod_test.ts":         runnerDeno,
		"swift test":                                 runnerSwift,
		"swift test --filter CalculatorTests":        runnerSwift,
		"bun test":                                   runnerBun,
		"bun test ./math.test.ts":                    runnerBun,
		"bun run test":                               runnerNone,
		"mocha":                                      runnerMocha,
		"npx mocha test/*.js":                        runnerMocha,
		"ctest":                                      runnerCTest,
		"ctest -R '^Foo$' --output-on-failure":       runnerCTest,
		"cmake --build . && ctest":                   runnerCTest,
		"playwright test":                            runnerPlaywright,
		"npx playwright test e2e/login.spec.ts":      runnerPlaywright,
		"rails test":                                 runnerMinitest,
		"rails test test/models/user_test.rb":        runnerMinitest,
		"rake test":                                  runnerMinitest,
		"bundle exec rake test TEST=test/x.rb":       runnerMinitest,
		"ruby -Itest test/calculator_test.rb":        runnerMinitest,
		"dart test":                                  runnerDart,
		"dart test test/calc_test.dart":              runnerDart,
		"flutter test":                               runnerDart,
		"flutter test test/widget_test.dart":         runnerDart,
		"julia test/runtests.jl":                     runnerJulia,
		"julia --project=. test/runtests.jl":         runnerJulia,
		"julia -e 'using Pkg; Pkg.test()'":           runnerJulia,
		"julia script.jl":                            runnerNone,
		"sbt test":                                   runnerScala,
		"sbt 'testOnly *CalculatorSpec'":             runnerScala,
		"./sbt test":                                 runnerScala,
		"lein test":                                  runnerClojure,
		"lein test :only myapp.core-test":            runnerClojure,
		"clojure -M:test":                            runnerClojure,
		"clj -X:test":                                runnerClojure,
		"kaocha":                                     runnerClojure,
		"lein kaocha --focus foo":                    runnerClojure,
		"clojure script.clj":                         runnerNone,
		"forge test":                                 runnerFoundry,
		"forge test --match-test test_Inc -vvv":      runnerFoundry,
		"ginkgo":                                     runnerGinkgo,
		"ginkgo run -r":                              runnerGinkgo,
		"ginkgo --focus 'Book'":                      runnerGinkgo,
		"go run github.com/onsi/ginkgo/v2/ginkgo -r": runnerGinkgo,
		"echo the ginkgoes sway":                     runnerNone,
		"jasmine":                                    runnerJasmine,
		"npx jasmine":                                runnerJasmine,
		"node_modules/.bin/jasmine spec/foo_spec.js": runnerJasmine,
		"Invoke-Pester":                              runnerPester,
		"Invoke-Pester -Path ./tests":                runnerPester,
		"pwsh -Command Invoke-Pester":                runnerPester,
		"pester":                                     runnerPester,
		"./build/calc_test --gtest_filter=Calc.*":    runnerGTest,
		"./mytest --gtest_color=no":                  runnerGTest,
		"build/run_tests --gtest_output=xml:r.xml":   runnerGTest,
		"robot tests/":                               runnerRobot,
		"python -m robot suite.robot":                runnerRobot,
		"pabot --processes 4 tests/":                 runnerRobot,
		"cucumber":                                   runnerCucumber,
		"bundle exec cucumber":                       runnerCucumber,
		"npx cucumber-js features/":                  runnerCucumber,
		"karma start karma.conf.js":                  runnerKarma,
		"npx karma start --single-run":               runnerKarma,
		"ng test":                                    runnerKarma,
		"ng test --watch=false":                      runnerKarma,
		"gotestsum":                                  runnerGotestsum,
		"gotestsum --format pkgname -- ./...":        runnerGotestsum,
		"gotestsum -- -run TestFoo ./pkg":            runnerGotestsum,
		"busted":                                     runnerBusted,
		"busted spec/":                               runnerBusted,
		"busted --output=TAP spec/foo_spec.lua":      runnerBusted,
		"stack test":                                 runnerHaskell,
		"stack test --fast":                          runnerHaskell,
		"cabal test":                                 runnerHaskell,
		"cabal v2-test":                              runnerHaskell,
		"cabal new-test all":                         runnerHaskell,
		"echo cabal build only":                      runnerNone,
		"crystal spec":                               runnerCrystal,
		"crystal spec spec/calc_spec.cr":             runnerCrystal,
		"crystal spec --verbose spec/":               runnerCrystal,
		"echo crystal build only":                    runnerNone,
		"echo the adjusted plan":                     runnerNone,
		"echo running test data":                     runnerNone,
		"echo about cucumbers":                       runnerNone,
		"echo the robotics demo":                     runnerNone,
		"echo the trumpeters tune up":                runnerNone,
		"echo forge testbed":                         runnerNone,
		"echo subtle differences":                    runnerNone,
		"echo rake testing notes":                    runnerNone,
		"ls -la":                                     runnerNone,
		"echo go testing the waters":                 runnerNone,
		"echo rspecs are great":                      runnerNone,
		"echo plan the upgrade":                      runnerNone,
		"echo swift testing guide":                   runnerNone,
	}
	for cmd, want := range cases {
		if got := classifyTestRunner(cmd); got != want {
			t.Errorf("classifyTestRunner(%q) = %v, want %v", cmd, got, want)
		}
	}
}

func TestParseGoTestFailures(t *testing.T) {
	out := `=== RUN   TestOK
--- PASS: TestOK (0.00s)
=== RUN   TestFoo
--- FAIL: TestFoo (0.01s)
    foo_test.go:42: expected 1, got 2
=== RUN   TestBar
--- FAIL: TestBar/sub (0.00s)
    bar_test.go:10: boom
--- FAIL: TestNoDetail (0.00s)
FAIL
FAIL	github.com/x/y	0.123s`
	got := parseTestFailures("go test ./...", out)
	want := []testFailure{
		{Name: "TestFoo", Detail: "foo_test.go:42: expected 1, got 2"},
		{Name: "TestBar/sub", Detail: "bar_test.go:10: boom"},
		{Name: "TestNoDetail"},
	}
	assertFailures(t, got, want)
}

func TestParseGoTestFailures_Panic(t *testing.T) {
	out := `=== RUN   TestPanics
--- FAIL: TestPanics (0.00s)
panic: boom [recovered]
	panic: boom

goroutine 19 [running]:
testing.tRunner.func1.2(...)
=== RUN   TestAssert
--- FAIL: TestAssert (0.00s)
    assert_test.go:7: expected 1, got 2`
	got := parseTestFailures("go test ./...", out)
	want := []testFailure{
		{Name: "TestPanics", Detail: "panic: boom"},
		{Name: "TestAssert", Detail: "assert_test.go:7: expected 1, got 2"},
	}
	assertFailures(t, got, want)
}

func TestParseGoTestFailures_UnrecoveredPanic(t *testing.T) {
	// A test that panics without recovering crashes the test binary before Go can
	// print a "--- FAIL:" line, so the only signal is the bare column-0 "panic:"
	// line plus the goroutine stack. The panicking test must be recovered from the
	// stack frame naming it, with the panic message as the detail.
	out := `=== RUN   TestExplodes
panic: runtime error: index out of range [3] with length 3

goroutine 6 [running]:
github.com/x/y.TestExplodes(0xc000102000)
	/home/x/y/explode_test.go:12 +0x18
testing.tRunner(0xc000102000, 0x5f1234)
	/usr/local/go/src/testing/testing.go:1576 +0x10b
created by testing.(*T).Run in goroutine 1
	/usr/local/go/src/testing/testing.go:1629 +0x3ea
exit status 2
FAIL	github.com/x/y	0.005s`
	got := parseTestFailures("go test ./...", out)
	want := []testFailure{
		{Name: "TestExplodes", Detail: "panic: runtime error: index out of range [3] with length 3"},
	}
	assertFailures(t, got, want)
}

func TestParseGoTestFailures_UnrecoveredPanicSubtestClosure(t *testing.T) {
	// A panic inside a subtest closure surfaces a "...TestName.funcN(" frame; the
	// parent test name (not the closure suffix) should be recovered, and a panic
	// with no test in its stack (a background goroutine) is left unattributed.
	out := `panic: send on closed channel

goroutine 9 [running]:
github.com/x/y.TestServer.func2(0xc000130000)
	/home/x/y/server_test.go:44 +0x5c
testing.tRunner(0xc000130000, 0xc00009e000)
	/usr/local/go/src/testing/testing.go:1576 +0x10b
exit status 2
FAIL	github.com/x/y	0.030s`
	got := parseTestFailures("go test ./...", out)
	want := []testFailure{
		{Name: "TestServer", Detail: "panic: send on closed channel"},
	}
	assertFailures(t, got, want)
}

func TestParseGoTestFailures_PanicNoTestInStack(t *testing.T) {
	// A panic whose stack names no test function (raised in an unrelated runtime
	// goroutine) cannot be attributed, so no spurious failure is invented.
	out := `panic: close of nil channel

goroutine 1 [running]:
main.run(...)
	/home/x/y/main.go:9
exit status 2`
	got := parseTestFailures("go test ./...", out)
	if len(got) != 0 {
		t.Fatalf("expected no failures for an unattributable panic, got %#v", got)
	}
}

func TestParseGoTestFailures_JSON(t *testing.T) {
	// `go test -json` wraps every line in an event object, so the text "--- FAIL:"
	// matcher never fires; the JSON parser keys on "fail" events instead and pulls
	// detail from the preceding "output" events.
	out := `{"Action":"run","Test":"TestOK"}
{"Action":"pass","Test":"TestOK","Elapsed":0}
{"Action":"run","Test":"TestFoo"}
{"Action":"output","Test":"TestFoo","Output":"    foo_test.go:42: expected 1, got 2\n"}
{"Action":"output","Test":"TestFoo","Output":"--- FAIL: TestFoo (0.01s)\n"}
{"Action":"fail","Test":"TestFoo","Elapsed":0.01}
{"Action":"run","Test":"TestNoDetail"}
{"Action":"fail","Test":"TestNoDetail","Elapsed":0}
{"Action":"fail","Package":"github.com/x/y","Elapsed":0.12}`
	got := parseTestFailures("go test -json ./...", out)
	want := []testFailure{
		{Name: "TestFoo", Detail: "foo_test.go:42: expected 1, got 2"},
		{Name: "TestNoDetail"},
	}
	assertFailures(t, got, want)
}

func TestParseGoTestFailures_JSONPanic(t *testing.T) {
	out := `{"Action":"run","Test":"TestPanics"}
{"Action":"output","Test":"TestPanics","Output":"panic: boom [recovered]\n"}
{"Action":"fail","Test":"TestPanics","Elapsed":0}`
	got := parseTestFailures("go test -json ./...", out)
	want := []testFailure{
		{Name: "TestPanics", Detail: "panic: boom"},
	}
	assertFailures(t, got, want)
}

func TestParseGoTestFailures_JSONBuildFailed(t *testing.T) {
	// `go test -json` emits no "--- FAIL:" or "[build failed]" text on a compile
	// error: the diagnostics arrive as package-scoped "output" events followed by
	// a package-level "fail" with no Test. The JSON parser must surface a
	// "pkg [build failed]" entry with the first diagnostic as detail.
	out := `{"Action":"output","Package":"github.com/x/y","Output":"# github.com/x/y [github.com/x/y.test]\n"}
{"Action":"output","Package":"github.com/x/y","Output":"./y_test.go:10:2: undefined: helper\n"}
{"Action":"output","Package":"github.com/x/y","Output":"./y_test.go:14:6: f declared and not used\n"}
{"Action":"fail","Package":"github.com/x/y","Elapsed":0}`
	got := parseTestFailures("go test -json ./...", out)
	want := []testFailure{
		{Name: "github.com/x/y [build failed]", Detail: "./y_test.go:10:2: undefined: helper"},
	}
	assertFailures(t, got, want)
}

func TestParseGoTestFailures_JSONTestFailNoBuildEntry(t *testing.T) {
	// A package-level "fail" that accompanies an individual test failure must not
	// also produce a spurious "[build failed]" entry: only the test is reported.
	out := `{"Action":"run","Test":"TestFoo"}
{"Action":"output","Test":"TestFoo","Output":"    foo_test.go:42: boom\n"}
{"Action":"fail","Test":"TestFoo","Package":"github.com/x/y","Elapsed":0}
{"Action":"fail","Package":"github.com/x/y","Elapsed":0}`
	got := parseTestFailures("go test -json ./...", out)
	want := []testFailure{
		{Name: "TestFoo", Detail: "foo_test.go:42: boom"},
	}
	assertFailures(t, got, want)
}

func TestParseGoTestFailures_BuildFailed(t *testing.T) {
	out := `# github.com/x/y [github.com/x/y.test]
./y_test.go:10:2: undefined: helper
./y_test.go:14:6: f declared and not used
FAIL	github.com/x/y [build failed]`
	got := parseTestFailures("go test ./...", out)
	want := []testFailure{
		{Name: "github.com/x/y [build failed]", Detail: "./y_test.go:10:2: undefined: helper"},
	}
	assertFailures(t, got, want)
}

func TestParseGoTestFailures_SetupFailed(t *testing.T) {
	out := "FAIL\tgithub.com/x/y [setup failed]"
	got := parseTestFailures("go test ./...", out)
	want := []testFailure{
		{Name: "github.com/x/y [setup failed]"},
	}
	assertFailures(t, got, want)
}

func TestParseGoTestFailures_BuildFailedPerPackage(t *testing.T) {
	// Each "# pkg" header scopes the compiler error that follows, so a build
	// failure picks up its own package's error rather than a stale one.
	out := `# github.com/x/a [github.com/x/a.test]
./a_test.go:3:2: undefined: a
FAIL	github.com/x/a [build failed]
# github.com/x/b [github.com/x/b.test]
./b_test.go:5:9: undefined: b
FAIL	github.com/x/b [build failed]`
	got := parseTestFailures("go test ./...", out)
	want := []testFailure{
		{Name: "github.com/x/a [build failed]", Detail: "./a_test.go:3:2: undefined: a"},
		{Name: "github.com/x/b [build failed]", Detail: "./b_test.go:5:9: undefined: b"},
	}
	assertFailures(t, got, want)
}

func TestParseGoTestNoFailures(t *testing.T) {
	out := "ok  \tgithub.com/x/y\t0.123s\n"
	if got := parseTestFailures("go test ./...", out); got != nil {
		t.Errorf("expected nil failures, got %v", got)
	}
}

func TestParseGotestsumFailures(t *testing.T) {
	// gotestsum's default pkgname reporter prints per-package status lines and
	// closes with a "=== Failed" summary block; the "--- FAIL:" framing lines are
	// stripped, so the parser keys on the "=== FAIL:" lines and reads the detail
	// from the indented output gotestsum reprints beneath each.
	out := `✓  pkg/calc (cached)
✖  pkg/calc

=== Failed
=== FAIL: pkg/calc TestAdd (0.00s)
    add_test.go:12: expected 3, got 2

=== FAIL: pkg/calc TestSub/negative (0.01s)
    sub_test.go:20: boom

DONE 5 tests, 2 failures in 0.123s`
	got := parseTestFailures("gotestsum --format pkgname -- ./...", out)
	want := []testFailure{
		{Name: "pkg/calc TestAdd", Detail: "add_test.go:12: expected 3, got 2"},
		{Name: "pkg/calc TestSub/negative", Detail: "sub_test.go:20: boom"},
	}
	assertFailures(t, got, want)
}

func TestParseGotestsumFailures_RerunAndPanic(t *testing.T) {
	// A flaky test re-run carries a " (re-run N)" suffix before the timing, and a
	// panicking test surfaces its "panic:" line as the detail.
	out := `=== Failed
=== FAIL: pkg/flaky TestFlaky (re-run 1) (0.00s)
panic: kaboom

=== FAIL: pkg/flaky TestOther (0.00s)
    other_test.go:3: nope`
	got := parseTestFailures("gotestsum -- ./...", out)
	want := []testFailure{
		{Name: "pkg/flaky TestFlaky", Detail: "panic: kaboom"},
		{Name: "pkg/flaky TestOther", Detail: "other_test.go:3: nope"},
	}
	assertFailures(t, got, want)
}

func TestParseGotestsumFailures_FallbackToGoText(t *testing.T) {
	// Run with the summary hidden (or a standard-verbose format), gotestsum emits
	// no "=== FAIL:" block — only the raw "--- FAIL:" lines — so the parser falls
	// back to the plain `go test` parser.
	out := `=== RUN   TestFoo
--- FAIL: TestFoo (0.01s)
    foo_test.go:42: expected 1, got 2
FAIL`
	got := parseTestFailures("gotestsum --no-summary -- ./...", out)
	want := []testFailure{
		{Name: "TestFoo", Detail: "foo_test.go:42: expected 1, got 2"},
	}
	assertFailures(t, got, want)
}

func TestParsePytestFailures_Summary(t *testing.T) {
	out := `tests/test_a.py .F
=================================== FAILURES ===================================
=========================== short test summary info ============================
FAILED tests/test_a.py::test_two - AssertionError: assert 1 == 2
FAILED tests/test_b.py::test_three
======================== 2 failed, 1 passed in 0.05s ===========================`
	got := parseTestFailures("pytest -q", out)
	want := []testFailure{
		{Name: "tests/test_a.py::test_two", Detail: "AssertionError: assert 1 == 2"},
		{Name: "tests/test_b.py::test_three"},
	}
	assertFailures(t, got, want)
}

func TestParsePytestFailures_SummaryErrors(t *testing.T) {
	// pytest reports collection/fixture errors as ERROR lines in the short
	// summary (alongside any FAILED lines); both must surface.
	out := `=========================== short test summary info ============================
FAILED tests/test_a.py::test_two - AssertionError: assert 1 == 2
ERROR tests/test_b.py::test_three - ValueError: boom
ERROR tests/test_c.py
======================== 1 failed, 2 errors in 0.05s ===========================`
	got := parseTestFailures("pytest -q", out)
	want := []testFailure{
		{Name: "tests/test_a.py::test_two", Detail: "AssertionError: assert 1 == 2"},
		{Name: "tests/test_b.py::test_three", Detail: "ValueError: boom"},
		{Name: "tests/test_c.py"},
	}
	assertFailures(t, got, want)
}

func TestParsePytestFailures_ParametrizedIDWithSpaces(t *testing.T) {
	// Parametrized node ids carry the param values in brackets, which routinely
	// contain spaces ("test_x[a b]") and may even contain the " - " detail
	// separator ("test_x[1 - 2]"). The whole id must stay together rather than
	// being clipped at the first space, and the trailing " - <message>" detail
	// must still split off after the bracketed section.
	out := `=========================== short test summary info ============================
FAILED tests/test_a.py::test_x[a b] - AssertionError: assert 1 == 2
FAILED tests/test_a.py::test_y[1 - 2]
ERROR tests/test_b.py::test_z[c d] - ValueError: boom`
	got := parseTestFailures("pytest -q", out)
	want := []testFailure{
		{Name: "tests/test_a.py::test_x[a b]", Detail: "AssertionError: assert 1 == 2"},
		{Name: "tests/test_a.py::test_y[1 - 2]"},
		{Name: "tests/test_b.py::test_z[c d]", Detail: "ValueError: boom"},
	}
	assertFailures(t, got, want)
}

func TestParsePytestFailures_VerboseParametrizedIDWithSpaces(t *testing.T) {
	// The verbose "<id> FAILED" form must likewise keep a bracketed param
	// section with spaces attached to the node id.
	out := `tests/test_a.py::test_x[a b] FAILED
tests/test_a.py::test_y[c d] ERROR`
	got := parseTestFailures("pytest -v", out)
	want := []testFailure{
		{Name: "tests/test_a.py::test_x[a b]"},
		{Name: "tests/test_a.py::test_y[c d]"},
	}
	assertFailures(t, got, want)
}

func TestParsePytestFailures_VerboseFallback(t *testing.T) {
	out := `tests/test_a.py::test_one PASSED
tests/test_a.py::test_two FAILED
tests/test_a.py::test_err ERROR`
	got := parseTestFailures("pytest -v", out)
	want := []testFailure{
		{Name: "tests/test_a.py::test_two"},
		{Name: "tests/test_a.py::test_err"},
	}
	assertFailures(t, got, want)
}

func TestParseToxFailures_Pytest(t *testing.T) {
	// tox runs pytest in its managed env and relays its output; the pytest
	// short-summary lines surface through the tox-routed parser. tox's own
	// per-environment "py311: FAILED ..." summary is indented and must not be
	// mistaken for a pytest "FAILED <id>" line.
	out := `py311 run-test: commands[0] | pytest -q
tests/test_a.py .F
=========================== short test summary info ============================
FAILED tests/test_a.py::test_two - AssertionError: assert 1 == 2
======================== 1 failed, 1 passed in 0.05s ===========================
ERROR: py311: commands failed
  py311: FAILED tests/test_a.py`
	got := parseTestFailures("tox -e py311", out)
	want := []testFailure{
		{Name: "tests/test_a.py::test_two", Detail: "AssertionError: assert 1 == 2"},
	}
	assertFailures(t, got, want)
}

func TestParseToxFailures_UnittestFallback(t *testing.T) {
	// A nox session that runs stdlib unittest carries no pytest summary, so the
	// tox-routed parser falls back to the unittest report.
	out := `nox > Running session tests
nox > python -m unittest
.F
======================================================================
FAIL: test_upper (test_module.TestStringMethods)
----------------------------------------------------------------------
Traceback (most recent call last):
  File "test_module.py", line 5, in test_upper
    self.assertEqual('foo'.upper(), 'FOOO')
AssertionError: 'FOO' != 'FOOO'

----------------------------------------------------------------------
Ran 2 tests in 0.001s

FAILED (failures=1)
nox > Session tests failed.`
	got := parseTestFailures("nox -s tests", out)
	want := []testFailure{
		{Name: "test_upper (test_module.TestStringMethods)", Detail: "AssertionError: 'FOO' != 'FOOO'"},
	}
	assertFailures(t, got, want)
}

func TestParseToxFailures_NoFailures(t *testing.T) {
	out := `py311 run-test: commands[0] | pytest -q
======================== 3 passed in 0.05s ===========================
  py311: OK`
	if got := parseTestFailures("tox", out); got != nil {
		t.Errorf("expected nil for passing tox run, got %v", got)
	}
}

func TestParseUnittestFailures(t *testing.T) {
	out := `..FE
======================================================================
FAIL: test_upper (test_module.TestStringMethods)
----------------------------------------------------------------------
Traceback (most recent call last):
  File "test_module.py", line 5, in test_upper
    self.assertEqual('foo'.upper(), 'FOOO')
AssertionError: 'FOO' != 'FOOO'

======================================================================
ERROR: test_boom (test_module.TestStringMethods)
----------------------------------------------------------------------
Traceback (most recent call last):
  File "test_module.py", line 9, in test_boom
    raise ValueError("nope")
ValueError: nope

----------------------------------------------------------------------
Ran 4 tests in 0.001s

FAILED (failures=1, errors=1)`
	got := parseTestFailures("python -m unittest test_module", out)
	want := []testFailure{
		{Name: "test_upper (test_module.TestStringMethods)", Detail: "AssertionError: 'FOO' != 'FOOO'"},
		{Name: "test_boom (test_module.TestStringMethods)", Detail: "ValueError: nope"},
	}
	assertFailures(t, got, want)
}

func TestParseUnittestFailures_NoDetail(t *testing.T) {
	// A bare assertion with no message still surfaces the test id even when the
	// traceback carries no recognizable exception line.
	out := `======================================================================
FAIL: test_x (mod.T)
----------------------------------------------------------------------
Traceback (most recent call last):
  File "mod.py", line 2, in test_x
    assert False
AssertionError

----------------------------------------------------------------------
Ran 1 test in 0.000s

FAILED (failures=1)`
	got := parseTestFailures("python -m unittest discover", out)
	want := []testFailure{
		{Name: "test_x (mod.T)", Detail: "AssertionError"},
	}
	assertFailures(t, got, want)
}

func TestParseUnittestFailures_Nose2(t *testing.T) {
	// nose2 renders results through unittest's TextTestResult, so its output
	// drives the same parser: the "nose2" command must be recognized and the
	// "FAIL:/ERROR: <id>" blocks parsed identically to `python -m unittest`.
	out := `.F
======================================================================
FAIL: test_add (tests.test_calc.CalcTests)
----------------------------------------------------------------------
Traceback (most recent call last):
  File "tests/test_calc.py", line 6, in test_add
    self.assertEqual(add(2, 2), 5)
AssertionError: 4 != 5

----------------------------------------------------------------------
Ran 2 tests in 0.001s

FAILED (failures=1)`
	got := parseTestFailures("nose2 -v tests", out)
	want := []testFailure{
		{Name: "test_add (tests.test_calc.CalcTests)", Detail: "AssertionError: 4 != 5"},
	}
	assertFailures(t, got, want)
}

func TestParseJestFailures(t *testing.T) {
	out := `  ✓ adds correctly (2 ms)
  ✕ subtracts correctly (3 ms)
  ✕ multiplies
  × divides (1 ms)`
	got := parseTestFailures("npm test", out)
	want := []testFailure{
		{Name: "subtracts correctly"},
		{Name: "multiplies"},
		{Name: "divides"},
	}
	assertFailures(t, got, want)
}

func TestParseJestFailures_WithDetail(t *testing.T) {
	out := ` FAIL  ./calc.test.js
  Calculator
    ✓ adds correctly (2 ms)
    ✕ subtracts correctly (3 ms)
    ✕ multiplies

  ● Calculator › subtracts correctly

    expect(received).toBe(expected)

    Expected: 1
    Received: 5

      8 |   expect(sub(3, 2)).toBe(1);

  ● Calculator › multiplies

    TypeError: mul is not a function
`
	got := parseTestFailures("npm test", out)
	want := []testFailure{
		{Name: "subtracts correctly", Detail: "expect(received).toBe(expected)"},
		{Name: "multiplies", Detail: "TypeError: mul is not a function"},
	}
	assertFailures(t, got, want)
}

func TestParseVitestFailures(t *testing.T) {
	// vitest's default reporter prints a per-file tree (with "×" leaf markers)
	// followed by a "Failed Tests" section whose blocks open with
	// "FAIL <file> > <suite> > <test>" and carry the assertion on the next line.
	out := ` ❯ src/calc.test.ts (3 tests | 2 failed)
   ✓ adds correctly
   × subtracts correctly
   × multiplies

⎯⎯⎯⎯⎯⎯ Failed Tests 2 ⎯⎯⎯⎯⎯⎯

 FAIL  src/calc.test.ts > Calculator > subtracts correctly
AssertionError: expected 1 to be 5

 FAIL  src/calc.test.ts > Calculator > multiplies
TypeError: mul is not a function
`
	got := parseTestFailures("npx vitest run", out)
	want := []testFailure{
		{Name: "subtracts correctly", Detail: "AssertionError: expected 1 to be 5"},
		{Name: "multiplies", Detail: "TypeError: mul is not a function"},
	}
	assertFailures(t, got, want)
}

func TestParseVitestFailures_HeaderOnly(t *testing.T) {
	// A reporter that omits the inline "×" tree still emits the "FAIL … > <test>"
	// block headers, so the failures are seeded from those alone.
	out := ` FAIL  test/api.test.ts > GET /users > returns 200
AssertionError: expected 500 to be 200
`
	got := parseTestFailures("vitest", out)
	want := []testFailure{
		{Name: "returns 200", Detail: "AssertionError: expected 500 to be 200"},
	}
	assertFailures(t, got, want)
}

func TestParseCargoTestFailures(t *testing.T) {
	out := `running 2 tests
test tests::ok ... ok
test tests::it_works ... FAILED

failures:

---- tests::it_works stdout ----
thread 'tests::it_works' panicked at 'assertion failed: left == right', src/lib.rs:10:5`
	got := parseTestFailures("cargo test", out)
	want := []testFailure{
		{Name: "tests::it_works", Detail: "'assertion failed: left == right', src/lib.rs:10:5"},
	}
	assertFailures(t, got, want)
}

func TestParseCargoTestFailures_Doctest(t *testing.T) {
	// A failing doctest names the source file, item, and line — the name carries
	// spaces, so the libtest "(\S+)" capture would miss it. The panic is reported
	// under thread 'main', so no detail is paired, but the name must surface.
	out := `running 1 test
test src/lib.rs - add (line 5) ... FAILED

failures:

---- src/lib.rs - add (line 5) stdout ----
Test executable failed (exit status: 101).

thread 'main' panicked at src/lib.rs:6:1:
assertion ` + "`left == right`" + ` failed`
	got := parseTestFailures("cargo test --doc", out)
	want := []testFailure{
		{Name: "src/lib.rs - add (line 5)"},
	}
	assertFailures(t, got, want)
}

func TestParseCargoTestFailures_BuildFailed(t *testing.T) {
	out := `   Compiling demo v0.1.0 (/tmp/demo)
error[E0425]: cannot find value ` + "`x`" + ` in this scope
 --> src/lib.rs:8:13
  |
8 |     let y = x + 1;
  |             ^ not found in this scope

error: aborting due to 1 previous error

error: could not compile ` + "`demo`" + ` (lib test) due to 1 previous error`
	got := parseTestFailures("cargo test", out)
	want := []testFailure{
		{Name: "demo (lib test) [build failed]", Detail: "error[E0425]: cannot find value `x` in this scope"},
	}
	assertFailures(t, got, want)
}

func TestParseCargoTestFailures_BuildFailedNoTarget(t *testing.T) {
	out := "error: could not compile `demo` due to 2 previous errors"
	got := parseTestFailures("cargo test", out)
	want := []testFailure{
		{Name: "demo [build failed]"},
	}
	assertFailures(t, got, want)
}

func TestParseNextestFailures(t *testing.T) {
	out := `    Starting 2 tests across 1 binary
        PASS [   0.004s] demo tests::ok
        FAIL [   0.005s] demo tests::it_works

--- STDERR:              demo tests::it_works ---
thread 'tests::it_works' panicked at src/lib.rs:10:5:
assertion failed: left == right

------------
     Summary [   0.006s] 2 tests run: 1 passed, 1 failed, 0 skipped
        FAIL [   0.005s] demo tests::it_works`
	got := parseTestFailures("cargo nextest run", out)
	want := []testFailure{
		{Name: "demo tests::it_works", Detail: "src/lib.rs:10:5:"},
	}
	assertFailures(t, got, want)
}

func TestParseNextestFailures_BuildFailed(t *testing.T) {
	out := `   Compiling demo v0.1.0 (/tmp/demo)
error[E0425]: cannot find value ` + "`x`" + ` in this scope

error: could not compile ` + "`demo`" + ` (lib test) due to 1 previous error`
	got := parseTestFailures("cargo nextest run", out)
	want := []testFailure{
		{Name: "demo (lib test) [build failed]", Detail: "error[E0425]: cannot find value `x` in this scope"},
	}
	assertFailures(t, got, want)
}

func TestParseRSpecFailures(t *testing.T) {
	out := `..F..F

Failures:

  1) Array#index_of returns -1 when the value is absent
     Failure/Error: expect(arr.index_of(5)).to eq(-1)

       expected: -1
            got: nil

  2) Calculator adds two numbers
     Failure/Error: expect(calc.add(1, 2)).to eq(4)

Finished in 0.01 seconds
6 examples, 2 failures

Failed examples:

rspec ./spec/array_spec.rb:10 # Array#index_of returns -1 when the value is absent
rspec ./spec/calc_spec.rb:7 # Calculator adds two numbers`
	got := parseTestFailures("bundle exec rspec", out)
	want := []testFailure{
		{Name: "Array#index_of returns -1 when the value is absent", Detail: "./spec/array_spec.rb:10"},
		{Name: "Calculator adds two numbers", Detail: "./spec/calc_spec.rb:7"},
	}
	assertFailures(t, got, want)
}

func TestParseRSpecFailures_NoSummaryFallback(t *testing.T) {
	// No "Failed examples:" section (e.g. an aborted run): fall back to the
	// numbered "Failures:" block, pairing each header with its Failure/Error line.
	out := `Failures:

  1) Array#index_of returns -1 when the value is absent
     Failure/Error: expect(arr.index_of(5)).to eq(-1)

  2) Calculator adds two numbers
     Failure/Error: expect(calc.add(1, 2)).to eq(4)`
	got := parseTestFailures("rspec", out)
	want := []testFailure{
		{Name: "Array#index_of returns -1 when the value is absent", Detail: "expect(arr.index_of(5)).to eq(-1)"},
		{Name: "Calculator adds two numbers", Detail: "expect(calc.add(1, 2)).to eq(4)"},
	}
	assertFailures(t, got, want)
}

func TestParsePHPUnitFailures(t *testing.T) {
	out := `PHPUnit 10.5.0 by Sebastian Bergmann and contributors.

..F.E                                                               5 / 5 (100%)

Time: 00:00.123, Memory: 8.00 MB

There was 1 failure:

1) App\Tests\MathTest::testAddition
Failed asserting that 4 matches expected 5.

/app/tests/MathTest.php:15

There was 1 error:

1) App\Tests\MathTest::testThrows
RuntimeException: boom

/app/tests/MathTest.php:20

FAILURES!
Tests: 5, Assertions: 4, Failures: 1, Errors: 1.`
	got := parseTestFailures("vendor/bin/phpunit", out)
	want := []testFailure{
		{Name: `App\Tests\MathTest::testAddition`, Detail: "Failed asserting that 4 matches expected 5."},
		{Name: `App\Tests\MathTest::testThrows`, Detail: "RuntimeException: boom"},
	}
	assertFailures(t, got, want)
}

func TestParsePHPUnitFailures_DataSet(t *testing.T) {
	out := `There was 1 failure:

1) MathTest::testAdd with data set #0 (1, 2, 4)
Failed asserting that 3 matches expected 4.

/app/tests/MathTest.php:30
`
	got := parseTestFailures("phpunit --testdox", out)
	want := []testFailure{
		{Name: "MathTest::testAdd with data set #0 (1, 2, 4)", Detail: "Failed asserting that 3 matches expected 4."},
	}
	assertFailures(t, got, want)
}

func TestParseDotnetTestFailures(t *testing.T) {
	out := `Starting test execution, please wait...
A total of 1 test files matched the specified pattern.
  Passed MyApp.Tests.CalcTests.Sub [2 ms]
  Failed MyApp.Tests.CalcTests.Add [4 ms]
  Error Message:
   Assert.Equal() Failure: Values differ
   Expected: 5
   Actual:   4
  Stack Trace:
     at MyApp.Tests.CalcTests.Add() in /src/CalcTests.cs:line 12

  Failed MyApp.Tests.CalcTests.Div(a: 1, b: 0) [< 1 ms]
  Error Message:
   System.DivideByZeroException : Attempted to divide by zero.
  Stack Trace:
     at MyApp.Calc.Div(Int32 a, Int32 b)

Failed!  - Failed:     2, Passed:     1, Skipped:     0, Total:     3`
	got := parseTestFailures("dotnet test", out)
	want := []testFailure{
		{Name: "MyApp.Tests.CalcTests.Add", Detail: "Assert.Equal() Failure: Values differ"},
		{Name: "MyApp.Tests.CalcTests.Div(a: 1, b: 0)", Detail: "System.DivideByZeroException : Attempted to divide by zero."},
	}
	assertFailures(t, got, want)
}

func TestParseDotnetTestFailures_NoDetail(t *testing.T) {
	out := `  Failed Suite.Tests.Lonely [3 ms]
Failed!  - Failed:     1, Passed:     0, Skipped:     0, Total:     1`
	got := parseTestFailures("dotnet test ./MyApp.sln", out)
	want := []testFailure{{Name: "Suite.Tests.Lonely"}}
	assertFailures(t, got, want)
}

func TestParseMavenTestFailures(t *testing.T) {
	out := `[INFO] -------------------------------------------------------
[INFO]  T E S T S
[INFO] -------------------------------------------------------
[INFO] Running com.example.CalculatorTest
[ERROR] Tests run: 3, Failures: 1, Errors: 1, Skipped: 0, Time elapsed: 0.041 s <<< FAILURE! - in com.example.CalculatorTest
[ERROR] com.example.CalculatorTest.testAdd  Time elapsed: 0.008 s  <<< FAILURE!
org.opentest4j.AssertionFailedError: expected: <5> but was: <4>
	at org.junit.jupiter.api.AssertionUtils.fail(AssertionUtils.java:55)
	at com.example.CalculatorTest.testAdd(CalculatorTest.java:12)

[ERROR] com.example.CalculatorTest.testDiv  Time elapsed: 0.002 s  <<< ERROR!
java.lang.ArithmeticException: / by zero
	at com.example.Calculator.div(Calculator.java:9)

[INFO] Results:
[INFO]
[ERROR] Failures:
[ERROR]   CalculatorTest.testAdd:12 expected: <5> but was: <4>`
	got := parseTestFailures("mvn test", out)
	want := []testFailure{
		{Name: "com.example.CalculatorTest.testAdd", Detail: "org.opentest4j.AssertionFailedError: expected: <5> but was: <4>"},
		{Name: "com.example.CalculatorTest.testDiv", Detail: "java.lang.ArithmeticException: / by zero"},
	}
	assertFailures(t, got, want)
}

func TestParseMavenTestFailures_JUnit4Name(t *testing.T) {
	// Older Surefire prints "method(FQCN)" and may omit a Maven log prefix.
	out := `Tests run: 1, Failures: 1, Errors: 0, Skipped: 0, Time elapsed: 0.03 sec <<< FAILURE! - in com.example.OldTest
testBar(com.example.OldTest)  Time elapsed: 0.01 sec  <<< FAILURE!
junit.framework.AssertionFailedError: nope
	at com.example.OldTest.testBar(OldTest.java:7)`
	got := parseTestFailures("./mvnw test", out)
	want := []testFailure{
		{Name: "testBar(com.example.OldTest)", Detail: "junit.framework.AssertionFailedError: nope"},
	}
	assertFailures(t, got, want)
}

func TestParseMavenTestFailures_NoFailures(t *testing.T) {
	out := `[INFO] Running com.example.OkTest
[INFO] Tests run: 2, Failures: 0, Errors: 0, Skipped: 0
[INFO] BUILD SUCCESS`
	if got := parseTestFailures("mvn test", out); got != nil {
		t.Errorf("expected nil for passing run, got %v", got)
	}
}

func TestParseGradleTestFailures(t *testing.T) {
	out := `> Task :compileJava
> Task :test

com.example.CalculatorTest > testAdd() FAILED
    org.opentest4j.AssertionFailedError: expected: <5> but was: <4>
        at app//org.junit.jupiter.api.AssertionUtils.fail(AssertionUtils.java:55)
        at app//com.example.CalculatorTest.testAdd(CalculatorTest.java:12)

com.example.CalculatorTest > testDiv() FAILED
    java.lang.ArithmeticException: / by zero
        at app//com.example.Calculator.div(Calculator.java:9)

3 tests completed, 2 failed

> Task :test FAILED`
	got := parseTestFailures("./gradlew test", out)
	want := []testFailure{
		{Name: "com.example.CalculatorTest > testAdd()", Detail: "org.opentest4j.AssertionFailedError: expected: <5> but was: <4>"},
		{Name: "com.example.CalculatorTest > testDiv()", Detail: "java.lang.ArithmeticException: / by zero"},
	}
	assertFailures(t, got, want)
}

func TestParseGradleTestFailures_NestedAndNoMessage(t *testing.T) {
	// A nested display name adds further " > " segments, and a failure whose body
	// is only stack frames yields no detail.
	out := `CalculatorTest > division > divides by zero FAILED
        at app//com.example.CalculatorTest.dividesByZero(CalculatorTest.java:20)

> Task :test FAILED`
	got := parseTestFailures("gradle test", out)
	want := []testFailure{
		{Name: "CalculatorTest > division > divides by zero"},
	}
	assertFailures(t, got, want)
}

func TestParseGradleTestFailures_NoFailures(t *testing.T) {
	out := `> Task :test

BUILD SUCCESSFUL in 2s`
	if got := parseTestFailures("gradle test", out); got != nil {
		t.Errorf("expected nil for passing run, got %v", got)
	}
}

func TestParseExUnitFailures(t *testing.T) {
	out := `..

  1) test adds two numbers (CalculatorTest)
     test/calculator_test.exs:8
     Assertion with == failed
     code:  assert Calculator.add(1, 2) == 4
     left:  3
     right: 4
     stacktrace:
       test/calculator_test.exs:9: (test)

  2) test divides by zero (CalculatorTest)
     test/calculator_test.exs:13
     ** (ArithmeticException) bad argument in arithmetic expression
     stacktrace:
       (calc 0.1.0) lib/calculator.ex:5: Calculator.div/2

Finished in 0.03 seconds
3 tests, 2 failures`
	got := parseTestFailures("mix test", out)
	want := []testFailure{
		{Name: "test adds two numbers (CalculatorTest)", Detail: "Assertion with == failed"},
		{Name: "test divides by zero (CalculatorTest)", Detail: "** (ArithmeticException) bad argument in arithmetic expression"},
	}
	assertFailures(t, got, want)
}

func TestParseExUnitFailures_LocationFallback(t *testing.T) {
	// A failure whose body carries only the source location (no message line)
	// falls back to that location as the detail.
	out := `  1) test something (MyTest)
     test/my_test.exs:42

1 test, 1 failure`
	got := parseTestFailures("mix test test/my_test.exs", out)
	want := []testFailure{
		{Name: "test something (MyTest)", Detail: "test/my_test.exs:42"},
	}
	assertFailures(t, got, want)
}

func TestParseExUnitFailures_NoFailures(t *testing.T) {
	out := `....

Finished in 0.02 seconds
4 tests, 0 failures`
	if got := parseTestFailures("mix test", out); got != nil {
		t.Errorf("expected nil for passing run, got %v", got)
	}
}

func TestParseTAPFailures(t *testing.T) {
	out := `TAP version 13
ok 1 - adds numbers
not ok 2 - subtracts numbers
  ---
  duration_ms: 0.5
  message: 'expected 1 to equal 2'
  ...
ok 3 - skipped one # SKIP not ready
not ok 4 - flaky one # TODO fix later
not ok 5 - divides numbers
  ---
  message: "division by zero"
  ...
1..5`
	got := parseTestFailures("node --test", out)
	want := []testFailure{
		{Name: "subtracts numbers", Detail: "expected 1 to equal 2"},
		{Name: "divides numbers", Detail: "division by zero"},
	}
	if fmt.Sprintf("%v", got) != fmt.Sprintf("%v", want) {
		t.Errorf("parseTAPFailures mismatch:\n got %v\nwant %v", got, want)
	}
}

func TestParseTAPFailures_NoMessageAndNoDescription(t *testing.T) {
	out := `not ok 1
not ok 2 - bare failure
1..2`
	got := parseTestFailures("tape test.js", out)
	want := []testFailure{
		{Name: "TAP test 1"},
		{Name: "bare failure"},
	}
	if fmt.Sprintf("%v", got) != fmt.Sprintf("%v", want) {
		t.Errorf("parseTAPFailures mismatch:\n got %v\nwant %v", got, want)
	}
}

func TestParseBatsFailures(t *testing.T) {
	// bats emits TAP but reports the failed expression and its location in "#"
	// comment lines rather than a YAML block, so the first comment is the detail.
	out := `1..2
ok 1 addition using bc
not ok 2 addition using dc
# (in test file test.bats, line 9)
#   ` + "`[ \"$result\" -eq 4 ]'" + ` failed
1..2`
	got := parseTestFailures("bats test.bats", out)
	want := []testFailure{
		{Name: "addition using dc", Detail: "(in test file test.bats, line 9)"},
	}
	if fmt.Sprintf("%v", got) != fmt.Sprintf("%v", want) {
		t.Errorf("parseBatsFailures mismatch:\n got %v\nwant %v", got, want)
	}
}

func TestParseProveFailures(t *testing.T) {
	// `prove -v` streams each Perl test file's raw TAP, including the "not ok"
	// lines and the "# Failed test ..." diagnostic comments Test::More prints
	// beneath a failure. The first "#" comment is the detail, as for bats.
	out := `t/calc.t ..
1..2
ok 1 - adds numbers
not ok 2 - subtracts numbers
#   Failed test 'subtracts numbers'
#   at t/calc.t line 12.
#          got: '1'
#     expected: '2'
t/calc.t .. Failed 1/2 subtests `
	got := parseTestFailures("prove -lv t/", out)
	want := []testFailure{
		{Name: "subtracts numbers", Detail: "Failed test 'subtracts numbers'"},
	}
	if fmt.Sprintf("%v", got) != fmt.Sprintf("%v", want) {
		t.Errorf("parseProveFailures mismatch:\n got %v\nwant %v", got, want)
	}
}

func TestParseTAPFailures_YAMLMessagePreferredOverComment(t *testing.T) {
	// When both a YAML "message:" field and "#" comments are present, the
	// structured message wins; the comment is only a fallback.
	out := `not ok 1 - subtracts numbers
# a stray diagnostic comment
  ---
  message: 'expected 1 to equal 2'
  ...
1..1`
	got := parseTestFailures("node --test", out)
	want := []testFailure{
		{Name: "subtracts numbers", Detail: "expected 1 to equal 2"},
	}
	if fmt.Sprintf("%v", got) != fmt.Sprintf("%v", want) {
		t.Errorf("parseTAPFailures mismatch:\n got %v\nwant %v", got, want)
	}
}

func TestParseTAPFailures_NoFailures(t *testing.T) {
	out := `TAP version 13
ok 1 - adds numbers
ok 2 - subtracts numbers
1..2`
	if got := parseTestFailures("node --test", out); got != nil {
		t.Errorf("expected nil for passing TAP run, got %v", got)
	}
}

func TestParseDenoTestFailures(t *testing.T) {
	out := `running 3 tests from ./math_test.ts
add ... ok (1ms)
subtract a number ... FAILED (2ms)
divide ... FAILED (1ms)

 ERRORS

subtract a number => ./math_test.ts:6:6
error: AssertionError: Values are not equal.
    at assertEquals (https://deno.land/std/assert/mod.ts:1:1)

divide => ./math_test.ts:12:6
error: Error: division by zero

 FAILURES

subtract a number => ./math_test.ts:6:6
divide => ./math_test.ts:12:6

FAILED | 1 passed | 2 failed (5ms)`
	got := parseTestFailures("deno test", out)
	want := []testFailure{
		{Name: "subtract a number", Detail: "AssertionError: Values are not equal."},
		{Name: "divide", Detail: "Error: division by zero"},
	}
	assertFailures(t, got, want)
}

func TestParseDenoTestFailures_NoErrorBlock(t *testing.T) {
	// Only the running outcome lines, no trailing ERRORS block: names still surface.
	out := `running 2 tests from ./mod_test.ts
works ... ok (0ms)
breaks ... FAILED (3ms)`
	got := parseTestFailures("deno test --allow-read", out)
	want := []testFailure{
		{Name: "breaks"},
	}
	assertFailures(t, got, want)
}

func TestParseDenoTestFailures_NoFailures(t *testing.T) {
	out := `running 2 tests from ./mod_test.ts
add ... ok (1ms)
subtract ... ok (0ms)

ok | 2 passed | 0 failed (2ms)`
	if got := parseTestFailures("deno test", out); got != nil {
		t.Errorf("expected nil for passing Deno run, got %v", got)
	}
}

func TestParseTestFailures_NonTestCommandIgnored(t *testing.T) {
	// Output contains FAIL/FAILED words but the command is not a test runner.
	out := "FAILED to connect\n--- FAIL: not a test"
	if got := parseTestFailures("curl http://x", out); got != nil {
		t.Errorf("expected nil for non-test command, got %v", got)
	}
}

func TestParseSwiftTestFailures_Linux(t *testing.T) {
	out := `Test Suite 'CalculatorTests' started at 2026-06-05 10:00:00.000
Test Case 'CalculatorTests.testAddition' started.
Test Case 'CalculatorTests.testAddition' passed (0.001 seconds).
Test Case 'CalculatorTests.testSubtraction' started.
/work/Tests/CalculatorTests/CalculatorTests.swift:22: error: CalculatorTests.testSubtraction : XCTAssertEqual failed: ("3") is not equal to ("4")
Test Case 'CalculatorTests.testSubtraction' failed (0.002 seconds).
Test Case 'CalculatorTests.testNoDetail' failed (0.000 seconds).`
	got := parseTestFailures("swift test", out)
	want := []testFailure{
		{Name: "CalculatorTests.testSubtraction", Detail: `XCTAssertEqual failed: ("3") is not equal to ("4")`},
		{Name: "CalculatorTests.testNoDetail"},
	}
	assertFailures(t, got, want)
}

func TestParseSwiftTestFailures_MacOS(t *testing.T) {
	out := `Test Case '-[CalculatorTests.CalculatorTests testDivide]' started.
/work/Tests/CalculatorTests/CalculatorTests.swift:30: error: -[CalculatorTests.CalculatorTests testDivide] : failed - division by zero
Test Case '-[CalculatorTests.CalculatorTests testDivide]' failed (0.003 seconds).`
	got := parseTestFailures("swift test --filter CalculatorTests", out)
	want := []testFailure{
		{Name: "-[CalculatorTests.CalculatorTests testDivide]", Detail: "failed - division by zero"},
	}
	assertFailures(t, got, want)
}

func TestParseSwiftTestFailures_NoFailures(t *testing.T) {
	out := `Test Case 'CalculatorTests.testAddition' started.
Test Case 'CalculatorTests.testAddition' passed (0.001 seconds).
Test Suite 'All tests' passed at 2026-06-05 10:00:01.000`
	if got := parseTestFailures("swift test", out); len(got) != 0 {
		t.Errorf("expected no failures, got %v", got)
	}
}

func TestParseSwiftTestFailures_SwiftTesting(t *testing.T) {
	out := `◇ Test run started.
◇ Suite CalculatorTests started.
◇ Test addition() started.
✔ Test addition() passed after 0.001 seconds.
◇ Test subtraction() started.
✘ Test subtraction() recorded an issue at CalculatorTests.swift:22:5: Expectation failed: (result → 3) == (expected → 4)
✘ Test subtraction() failed after 0.002 seconds with 1 issue.
✘ Test crash() recorded an issue: Caught error: boom
✘ Test crash() failed after 0.000 seconds with 1 issue.
✘ Test run with 3 tests failed after 0.010 seconds with 2 issues.`
	got := parseTestFailures("swift test", out)
	want := []testFailure{
		{Name: "subtraction()", Detail: "Expectation failed: (result → 3) == (expected → 4)"},
		{Name: "crash()", Detail: "Caught error: boom"},
	}
	assertFailures(t, got, want)
}

// A single `swift test` run can drive both XCTest and Swift Testing suites; both
// failure shapes must surface from one parse.
func TestParseSwiftTestFailures_MixedFrameworks(t *testing.T) {
	out := `Test Case 'LegacyTests.testOld' started.
/work/Tests/LegacyTests.swift:10: error: LegacyTests.testOld : XCTAssertTrue failed
Test Case 'LegacyTests.testOld' failed (0.001 seconds).
✘ Test modern() recorded an issue at ModernTests.swift:5:3: Expectation failed: 1 == 2
✘ Test modern() failed after 0.001 seconds with 1 issue.`
	got := parseTestFailures("swift test", out)
	want := []testFailure{
		{Name: "LegacyTests.testOld", Detail: "XCTAssertTrue failed"},
		{Name: "modern()", Detail: "Expectation failed: 1 == 2"},
	}
	assertFailures(t, got, want)
}

func TestParseBunTestFailures(t *testing.T) {
	out := `bun test v1.1.0

math.test.ts:
✓ adds two numbers [0.42ms]
✗ subtracts two numbers [0.31ms]
✗ math > divides two numbers [1.20s]

 5 |   expect(subtract(5, 3)).toBe(1);
                              ^
error: expect(received).toBe(expected)

 2 pass
 2 fail`
	got := parseTestFailures("bun test", out)
	want := []testFailure{
		{Name: "subtracts two numbers"},
		{Name: "math > divides two numbers"},
	}
	assertFailures(t, got, want)
}

func TestParseBunTestFailures_NoFailures(t *testing.T) {
	out := `math.test.ts:
✓ adds two numbers [0.42ms]

 1 pass
 0 fail`
	if got := parseTestFailures("bun test", out); len(got) != 0 {
		t.Errorf("expected no failures, got %v", got)
	}
}

func TestParseDartTestFailures(t *testing.T) {
	out := `00:00 +0: loading test/calc_test.dart
00:00 +0: adds two numbers
00:01 +1: subtracts two numbers
00:01 +1 -1: subtracts two numbers [E]
  Expected: <2>
    Actual: <1>

  package:test_api          expect
  test/calc_test.dart 9:5   main.<fn>

00:01 +1 -1: math group divides by zero [E]
  ArgumentError: cannot divide by zero

00:01 +1 -2: Some tests failed.`
	got := parseTestFailures("dart test", out)
	want := []testFailure{
		{Name: "subtracts two numbers", Detail: "Expected: <2>"},
		{Name: "math group divides by zero", Detail: "ArgumentError: cannot divide by zero"},
	}
	assertFailures(t, got, want)
}

func TestParseDartTestFailures_SkippedCounter(t *testing.T) {
	// A "~skipped" counter segment must not break the failure-line match, and a
	// test that emits two error lines is reported once.
	out := `00:02 +3 ~1 -1: widget renders title [E]
  Expected a Text widget, found none.
00:02 +3 ~1 -1: widget renders title [E]
  (second failure on the same test)
00:02 +3 ~1 -1: Some tests failed.`
	got := parseTestFailures("flutter test", out)
	want := []testFailure{
		{Name: "widget renders title", Detail: "Expected a Text widget, found none."},
	}
	assertFailures(t, got, want)
}

func TestParseDartTestFailures_NoFailures(t *testing.T) {
	out := `00:00 +0: adds two numbers
00:01 +2: All tests passed!`
	if got := parseTestFailures("dart test", out); len(got) != 0 {
		t.Errorf("expected no failures, got %v", got)
	}
}

func TestParseJuliaTestFailures(t *testing.T) {
	out := `Test Summary: | Pass  Fail  Error  Total
Arithmetic    |    1     1      1      3
add: Test Failed at /home/u/proj/test/runtests.jl:7
  Expression: add(2, 2) == 5
   Evaluated: 4 == 5
Stacktrace:
 [1] macro expansion
   @ /usr/share/julia/stdlib/Test/src/Test.jl:679 [inlined]
Error During Test at /home/u/proj/test/runtests.jl:12
  Test threw exception
  Expression: divide(1, 0)
  DivideError: integer division error
ERROR: LoadError: Some tests did not pass: 1 passed, 1 failed, 1 errored.`
	got := parseTestFailures("julia test/runtests.jl", out)
	want := []testFailure{
		{Name: "/home/u/proj/test/runtests.jl:7", Detail: "add(2, 2) == 5"},
		{Name: "/home/u/proj/test/runtests.jl:12", Detail: "divide(1, 0)"},
	}
	assertFailures(t, got, want)
}

func TestParseJuliaTestFailures_PkgTest(t *testing.T) {
	// The `Pkg.test()` invocation routes to the same parser, and a failure block
	// without an "Expression:" line surfaces with no detail rather than borrowing
	// the next block's.
	out := `Test Failed at /pkg/test/runtests.jl:3
   Evaluated: false
Test Failed at /pkg/test/runtests.jl:9
  Expression: occursin("x", "y")`
	got := parseTestFailures("julia -e 'using Pkg; Pkg.test()'", out)
	want := []testFailure{
		{Name: "/pkg/test/runtests.jl:3"},
		{Name: "/pkg/test/runtests.jl:9", Detail: `occursin("x", "y")`},
	}
	assertFailures(t, got, want)
}

func TestParseJuliaTestFailures_NoFailures(t *testing.T) {
	out := `Test Summary: | Pass  Total
Arithmetic    |    3      3`
	if got := parseTestFailures("julia test/runtests.jl", out); len(got) != 0 {
		t.Errorf("expected no failures, got %v", got)
	}
}

func TestParseMochaFailures(t *testing.T) {
	out := `  Array
    #indexOf()
      ✓ returns the index when present
      1) returns -1 when not present
  Math
      2) adds

  1 passing (12ms)
  2 failing

  1) Array
       #indexOf()
         returns -1 when not present:

      AssertionError: expected -1 to equal 0
      + expected - actual

      at Context.<anonymous> (test/array.test.js:8:25)

  2) Math
       adds:
      TypeError: add is not a function
      at Context.<anonymous> (test/math.test.js:4:10)
`
	got := parseTestFailures("npx mocha", out)
	want := []testFailure{
		{Name: "Array #indexOf() returns -1 when not present", Detail: "AssertionError: expected -1 to equal 0"},
		{Name: "Math adds", Detail: "TypeError: add is not a function"},
	}
	assertFailures(t, got, want)
}

func TestParseMochaFailures_FlatTitle(t *testing.T) {
	// A test with no surrounding describe block: the title sits on the header line
	// itself, already ending in a colon.
	out := `  1 failing

  1) should work:
     Error: boom
`
	got := parseTestFailures("mocha", out)
	want := []testFailure{
		{Name: "should work", Detail: "Error: boom"},
	}
	assertFailures(t, got, want)
}

func TestParseCypressFailures(t *testing.T) {
	// `cypress run` emits Mocha's spec-reporter output, so it routes to the Mocha
	// parser. The numbered "N) <title>" block (title ending in a colon) followed by
	// the assertion message is the same shape the Mocha parser handles.
	out := `  Running:  login.cy.js

  Login flow
    ✓ shows the form (52ms)
    1) rejects a bad password

  1 passing (1s)
  1 failing

  1) Login flow
       rejects a bad password:
     AssertionError: expected 'logged in' to equal 'error'
      at Context.eval (webpack:///./cypress/e2e/login.cy.js:12:30)
`
	got := parseTestFailures("npx cypress run", out)
	want := []testFailure{
		{Name: "Login flow rejects a bad password", Detail: "AssertionError: expected 'logged in' to equal 'error'"},
	}
	assertFailures(t, got, want)
}

func TestParseHardhatFailures(t *testing.T) {
	// `hardhat test` drives Mocha with the spec reporter, so it routes to the Mocha
	// parser: the numbered "N) <title>" block (title ending in a colon) followed by
	// the assertion message is the same shape the Mocha parser handles.
	out := `  Token contract
    ✓ assigns the total supply to the owner (89ms)
    1) reverts a transfer that exceeds the balance

  1 passing (1s)
  1 failing

  1) Token contract
       reverts a transfer that exceeds the balance:
     AssertionError: Expected transaction to be reverted with reason 'insufficient balance'
      at Context.<anonymous> (test/Token.js:24:7)
`
	got := parseTestFailures("npx hardhat test", out)
	want := []testFailure{
		{Name: "Token contract reverts a transfer that exceeds the balance", Detail: "AssertionError: Expected transaction to be reverted with reason 'insufficient balance'"},
	}
	assertFailures(t, got, want)
}

func TestParseHardhatFailures_NoFailures(t *testing.T) {
	out := `  Token contract
    ✓ assigns the total supply to the owner (89ms)

  1 passing (1s)
`
	if got := parseTestFailures("hardhat test", out); len(got) != 0 {
		t.Errorf("expected no failures, got %v", got)
	}
}

func TestParseCypressFailures_NoFailures(t *testing.T) {
	out := `  Login flow
    ✓ shows the form (52ms)

  1 passing (1s)
`
	if got := parseTestFailures("cypress run", out); len(got) != 0 {
		t.Errorf("expected no failures, got %v", got)
	}
}

func TestParseMochaFailures_NoFailures(t *testing.T) {
	out := `  Array
    ✓ works

  1 passing (3ms)
`
	if got := parseTestFailures("mocha", out); len(got) != 0 {
		t.Errorf("expected no failures, got %v", got)
	}
}

func TestParseBustedFailures(t *testing.T) {
	// Busted's utfTerminal reporter closes a failing run with a block per failure:
	// a "Failure → <file> @ <line>" header, the full description, then the
	// "<file>:<line>: <message>" assertion. An "Error → ..." block (an uncaught Lua
	// error) is recognized the same way.
	out := `◼●◼
1 success / 1 failure / 1 error / 0 pending : 0.001 seconds

Failure → spec/example_spec.lua @ 4
Calculator adds two numbers
spec/example_spec.lua:5: Expected objects to be equal.
Passed in:
(number) 2
Expected:
(number) 3

Error → spec/example_spec.lua @ 10
Calculator divides safely
spec/example_spec.lua:11: attempt to perform arithmetic on a nil value`
	got := parseTestFailures("busted spec/", out)
	want := []testFailure{
		{Name: "Calculator adds two numbers", Detail: "spec/example_spec.lua:5: Expected objects to be equal."},
		{Name: "Calculator divides safely", Detail: "spec/example_spec.lua:11: attempt to perform arithmetic on a nil value"},
	}
	assertFailures(t, got, want)
}

func TestParseBustedFailures_PlainArrowAndNoDetail(t *testing.T) {
	// The plainTerminal handler spells the arrow "->", and a failure whose block
	// carries no "<file>:<line>:" line surfaces with the description but no detail
	// rather than borrowing the next block's.
	out := `Failure -> spec/foo_spec.lua @ 7
a thing without a located assertion

Failure -> spec/foo_spec.lua @ 12
another thing
spec/foo_spec.lua:13: boom`
	got := parseTestFailures("busted", out)
	want := []testFailure{
		{Name: "a thing without a located assertion"},
		{Name: "another thing", Detail: "spec/foo_spec.lua:13: boom"},
	}
	assertFailures(t, got, want)
}

func TestParseBustedFailures_NoFailures(t *testing.T) {
	out := `●●●
3 successes / 0 failures / 0 errors / 0 pending : 0.002 seconds`
	if got := parseTestFailures("busted spec/", out); len(got) != 0 {
		t.Errorf("expected no failures, got %v", got)
	}
}

func TestParseHaskellFailures(t *testing.T) {
	// hspec closes a failing run with a "Failures:" section that numbers each
	// failed example ("N) <description>") and prints its source location on the
	// indented line directly above. The description becomes the name and that
	// location the detail.
	out := `
Math.Addition
  adds two numbers [✔]
  is commutative [✘]
Math.Division
  divides safely [✘]

Failures:

  test/MathSpec.hs:14:7:
  1) Math.Addition is commutative
       expected: 5
        but got: 6

  test/MathSpec.hs:22:3:
  2) Math.Division divides safely
       uncaught exception: DivideByZero

  To rerun use: --match "/Math.Addition/is commutative/"

Randomized with seed 1234

Finished in 0.0031 seconds
3 examples, 2 failures`
	got := parseTestFailures("stack test", out)
	want := []testFailure{
		{Name: "Math.Addition is commutative", Detail: "test/MathSpec.hs:14:7"},
		{Name: "Math.Division divides safely", Detail: "test/MathSpec.hs:22:3"},
	}
	assertFailures(t, got, want)
}

func TestParseHaskellFailures_NoLocation(t *testing.T) {
	// A failure whose block carries no source-location line surfaces with the
	// description but no detail rather than borrowing the previous block's.
	out := `Failures:

  test/Spec.hs:3:1:
  1) located failure
       expected: True

  2) unlocated failure
       expected: ok

2 examples, 2 failures`
	got := parseTestFailures("cabal test", out)
	want := []testFailure{
		{Name: "located failure", Detail: "test/Spec.hs:3:1"},
		{Name: "unlocated failure"},
	}
	assertFailures(t, got, want)
}

func TestParseHaskellFailures_NoFailuresSection(t *testing.T) {
	// Without a "Failures:" header (a passing run, or a run driving a different
	// framework such as tasty) nothing is reported, even when numbered lines
	// appear elsewhere in the output.
	out := `Math.Addition
  1 adds two numbers [✔]

Finished in 0.0009 seconds
1 example, 0 failures`
	if got := parseTestFailures("stack test", out); len(got) != 0 {
		t.Errorf("expected no failures, got %v", got)
	}
}

func TestParseBazelFailures(t *testing.T) {
	// Bazel's run summary lists each test target with a right-padded status row;
	// a FAILED row names the failing target and prints its log path on the
	// indented line beneath, which becomes the detail.
	out := `INFO: Analyzed 3 targets (0 packages loaded, 0 targets configured).
INFO: Found 3 test targets...
//pkg/math:add_test                                              PASSED in 0.1s
//pkg/math:sub_test                                              FAILED in 0.3s
  /home/user/.cache/bazel/_bazel_user/abc/execroot/ws/bazel-out/k8-fastbuild/testlogs/pkg/math/sub_test/test.log
//pkg/str:join_test                                              FAILED in 2 out of 3 in 0.5s
  /home/user/.cache/bazel/_bazel_user/abc/execroot/ws/bazel-out/k8-fastbuild/testlogs/pkg/str/join_test/test.log

INFO: Build completed, 2 tests FAILED, 3 total actions
Executed 3 out of 3 tests: 1 test passes and 2 fail locally.`
	got := parseTestFailures("bazel test //...", out)
	want := []testFailure{
		{Name: "//pkg/math:sub_test", Detail: "/home/user/.cache/bazel/_bazel_user/abc/execroot/ws/bazel-out/k8-fastbuild/testlogs/pkg/math/sub_test/test.log"},
		{Name: "//pkg/str:join_test", Detail: "/home/user/.cache/bazel/_bazel_user/abc/execroot/ws/bazel-out/k8-fastbuild/testlogs/pkg/str/join_test/test.log"},
	}
	assertFailures(t, got, want)
}

func TestParseBazelFailures_DedupAndNoLog(t *testing.T) {
	// A target's FAILED row can be streamed during the run and repeated in the
	// final summary; it is reported once. A row with no following log line still
	// surfaces, with no detail. The bazelisk wrapper and "coverage" subcommand
	// are recognized too.
	out := `//app:e2e_test                                                   FAILED in 1.2s
  /tmp/testlogs/app/e2e_test/test.log
//app:e2e_test                                                   FAILED in 1.2s
  /tmp/testlogs/app/e2e_test/test.log
//app:smoke_test                                                 FAILED
Executed 2 out of 2 tests: 2 fail locally.`
	got := parseTestFailures("bazelisk coverage //app/...", out)
	want := []testFailure{
		{Name: "//app:e2e_test", Detail: "/tmp/testlogs/app/e2e_test/test.log"},
		{Name: "//app:smoke_test"},
	}
	assertFailures(t, got, want)
}

func TestParseBazelFailures_NoFailures(t *testing.T) {
	// An all-passing run prints only PASSED rows, which must not be reported, and
	// a "bazel build" invocation is not a test run at all.
	out := `//pkg:a_test                                                     PASSED in 0.1s
//pkg:b_test                                                     PASSED in 0.2s
Executed 2 out of 2 tests: 2 tests pass.`
	if got := parseTestFailures("bazel test //...", out); len(got) != 0 {
		t.Errorf("expected no failures, got %v", got)
	}
	if got := parseTestFailures("bazel build //...", out); got != nil {
		t.Errorf("bazel build is not a test runner, got %v", got)
	}
}

func TestParseCrystalFailures(t *testing.T) {
	out := `..F.F

Failures:

  1) Calculator adds two numbers
       Expected: 4
            got: 3

     # spec/calc_spec.cr:7

  2) Array#index_of returns -1 when absent
       Expected: -1
            got: nil

     # spec/array_spec.cr:10

Finished in 1.2 milliseconds
4 examples, 2 failures, 0 errors, 0 pending

Failed examples:

crystal spec spec/calc_spec.cr:7 # Calculator adds two numbers
crystal spec spec/array_spec.cr:10 # Array#index_of returns -1 when absent`
	got := parseTestFailures("crystal spec", out)
	want := []testFailure{
		{Name: "Calculator adds two numbers", Detail: "spec/calc_spec.cr:7"},
		{Name: "Array#index_of returns -1 when absent", Detail: "spec/array_spec.cr:10"},
	}
	assertFailures(t, got, want)
}

func TestParseCrystalFailures_NoSummaryFallback(t *testing.T) {
	// No "Failed examples:" section (e.g. an aborted run): fall back to the
	// numbered "Failures:" block, pairing each header with its first
	// Expected:/Unhandled exception: body line as the detail.
	out := `Failures:

  1) Calculator adds two numbers
       Expected: 4
            got: 3

  2) Service raises on bad input
       Unhandled exception: boom (RuntimeError)`
	got := parseTestFailures("crystal spec spec/", out)
	want := []testFailure{
		{Name: "Calculator adds two numbers", Detail: "Expected: 4"},
		{Name: "Service raises on bad input", Detail: "Unhandled exception: boom (RuntimeError)"},
	}
	assertFailures(t, got, want)
}

func TestParseCrystalFailures_NoFailures(t *testing.T) {
	// An all-passing run prints no "Failed examples:" block and no numbered
	// "Failures:" entries, so nothing must be reported.
	out := `....

Finished in 0.8 milliseconds
4 examples, 0 failures, 0 errors, 0 pending`
	if got := parseTestFailures("crystal spec", out); len(got) != 0 {
		t.Errorf("expected no failures, got %v", got)
	}
}

func TestSummarizeTestFailures(t *testing.T) {
	if s := summarizeTestFailures(nil); s != "" {
		t.Errorf("empty summary want \"\", got %q", s)
	}
	s := summarizeTestFailures([]testFailure{
		{Name: "TestFoo", Detail: "foo_test.go:42: boom"},
		{Name: "TestBar"},
	})
	want := "[test failures: 2]\n  TestFoo — foo_test.go:42: boom\n  TestBar"
	if s != want {
		t.Errorf("summary mismatch:\n got %q\nwant %q", s, want)
	}
}

func TestSummarizeTestFailuresClipsLongDetail(t *testing.T) {
	// A single assertion can print a multi-hundred-character expected-vs-actual
	// line; the inline summary clips it to maxFailureDetailWidth runes plus an
	// ellipsis, while the caller keeps the untruncated detail in metadata.
	long := strings.Repeat("x", maxFailureDetailWidth+50)
	s := summarizeTestFailures([]testFailure{{Name: "TestBig", Detail: long}})

	clipped := strings.Repeat("x", maxFailureDetailWidth) + "…"
	want := "[test failures: 1]\n  TestBig — " + clipped
	if s != want {
		t.Errorf("summary mismatch:\n got %q\nwant %q", s, want)
	}
	if strings.Contains(s, long) {
		t.Error("summary should not contain the untruncated detail")
	}
}

func TestSummarizeTestFailuresKeepsShortDetail(t *testing.T) {
	// A detail at exactly the width limit is emitted verbatim, with no ellipsis.
	exact := strings.Repeat("y", maxFailureDetailWidth)
	s := summarizeTestFailures([]testFailure{{Name: "TestEdge", Detail: exact}})
	if strings.Contains(s, "…") {
		t.Errorf("detail at the width limit should not be clipped:\n%s", s)
	}
	if !strings.HasSuffix(s, exact) {
		t.Errorf("detail at the width limit should be emitted verbatim:\n%s", s)
	}
}

func TestTruncateDetailRuneSafe(t *testing.T) {
	// Truncation must land on a rune boundary so multi-byte content is never
	// split mid-character. A string of N+10 multi-byte runes clips to exactly
	// maxFailureDetailWidth runes plus the ellipsis, all valid UTF-8.
	detail := strings.Repeat("é", maxFailureDetailWidth+10)
	got := truncateDetail(detail)
	if !utf8.ValidString(got) {
		t.Fatalf("truncated detail is not valid UTF-8: %q", got)
	}
	if want := maxFailureDetailWidth + utf8.RuneCountInString("…"); utf8.RuneCountInString(got) != want {
		t.Errorf("rune count = %d, want %d", utf8.RuneCountInString(got), want)
	}
}

func TestSummarizeTestFailuresCapsLongLists(t *testing.T) {
	// A wholesale breakage produces far more failures than the inline cap; the
	// summary lists maxSummarizedFailures of them, reports the true total in the
	// header, and collapses the rest into an explicit "... and N more" marker.
	total := maxSummarizedFailures + 7
	failures := make([]testFailure, total)
	for i := range failures {
		failures[i] = testFailure{Name: fmt.Sprintf("Test%03d", i)}
	}
	s := summarizeTestFailures(failures)

	if want := fmt.Sprintf("[test failures: %d]", total); !strings.HasPrefix(s, want) {
		t.Errorf("header = %q, want prefix %q", s, want)
	}
	lines := strings.Split(s, "\n")
	// header + maxSummarizedFailures entries + the "... and N more" line.
	if wantLines := 1 + maxSummarizedFailures + 1; len(lines) != wantLines {
		t.Fatalf("line count = %d, want %d", len(lines), wantLines)
	}
	if last := lines[len(lines)-1]; last != "  ... and 7 more" {
		t.Errorf("trailer = %q, want %q", last, "  ... and 7 more")
	}
	// The last shown entry is the one immediately before the cap; entries past it
	// must not appear inline.
	if strings.Contains(s, fmt.Sprintf("Test%03d", maxSummarizedFailures)) {
		t.Errorf("summary should not contain the first elided entry:\n%s", s)
	}
}

func TestSummarizeTestFailuresNoCapAtBoundary(t *testing.T) {
	// Exactly maxSummarizedFailures failures fit without a "... and N more" line.
	failures := make([]testFailure, maxSummarizedFailures)
	for i := range failures {
		failures[i] = testFailure{Name: fmt.Sprintf("Test%03d", i)}
	}
	s := summarizeTestFailures(failures)
	if strings.Contains(s, "more") {
		t.Errorf("boundary summary should not be truncated:\n%s", s)
	}
	if got := strings.Count(s, "\n"); got != maxSummarizedFailures {
		t.Errorf("entry line count = %d, want %d", got, maxSummarizedFailures)
	}
}

func TestParseCTestFailures(t *testing.T) {
	out := `Test project /home/user/build
    Start 1: PassTest
1/3 Test #1: PassTest .........................   Passed    0.01 sec
    Start 2: FailTest
2/3 Test #2: FailTest ......................***Failed    0.02 sec
    Start 3: SlowTest
3/3 Test #3: SlowTest ......................***Timeout  10.00 sec

33% tests passed, 2 tests failed out of 3

The following tests FAILED:
	  2 - FailTest (Failed)
	  3 - SlowTest (Timeout)
Errors while running CTest`
	got := parseTestFailures("ctest --output-on-failure", out)
	want := []testFailure{
		{Name: "FailTest", Detail: "Failed"},
		{Name: "SlowTest", Detail: "Timeout"},
	}
	assertFailures(t, got, want)
}

func TestParseCTestFailures_NoFailures(t *testing.T) {
	// A clean run prints no "The following tests FAILED:" block, so the per-test
	// "Passed" progress lines must not be mistaken for failures.
	out := `Test project /home/user/build
1/1 Test #1: PassTest .........................   Passed    0.01 sec

100% tests passed, 0 tests failed out of 1`
	if got := parseTestFailures("ctest", out); len(got) != 0 {
		t.Errorf("expected no failures, got %v", got)
	}
}

func TestParseGTestFailures(t *testing.T) {
	out := `[==========] Running 3 tests from 1 test suite.
[----------] Global test environment set-up.
[----------] 3 tests from CalcTest
[ RUN      ] CalcTest.Adds
[       OK ] CalcTest.Adds (0 ms)
[ RUN      ] CalcTest.Subtracts
../calc_test.cc:18: Failure
Expected equality of these values:
  Subtract(5, 3)
    Which is: 1
  2
[  FAILED  ] CalcTest.Subtracts (1 ms)
[ RUN      ] CalcTest.Divides
unknown file: Failure
C++ exception with description "divide by zero" thrown in the test body.
[  FAILED  ] CalcTest.Divides (0 ms)
[----------] 3 tests from CalcTest (2 ms total)

[==========] 3 tests from 1 test suite ran. (2 ms total)
[  PASSED  ] 1 test.
[  FAILED  ] 2 tests, listed below:
[  FAILED  ] CalcTest.Subtracts
[  FAILED  ] CalcTest.Divides

 2 FAILED TESTS`
	got := parseTestFailures("./build/calc_test --gtest_color=no", out)
	want := []testFailure{
		{Name: "CalcTest.Subtracts", Detail: "../calc_test.cc:18: Failure"},
		// The "Divides" body opens with an "unknown file: Failure" header (a thrown
		// exception, not a located assertion); it has no "<file>:<line>:" prefix, so
		// no detail is attached — the failure is still reported.
		{Name: "CalcTest.Divides"},
	}
	assertFailures(t, got, want)
}

func TestParseGTestFailures_Parameterized(t *testing.T) {
	// Value/type-parameterized test names carry slashes; they must survive intact,
	// and the summary block's "[  FAILED  ] N tests, listed below:" header (whose
	// first token is a bare number, no dot) must not be mistaken for a failure.
	out := `[ RUN      ] Inst/CalcTest.Adds/0
../calc_test.cc:7: Failure
[  FAILED  ] Inst/CalcTest.Adds/0, where GetParam() = 4 (0 ms)
[  FAILED  ] 1 test, listed below:
[  FAILED  ] Inst/CalcTest.Adds/0, where GetParam() = 4

 1 FAILED TEST`
	got := parseTestFailures("mytest --gtest_filter=Inst/*", out)
	want := []testFailure{
		{Name: "Inst/CalcTest.Adds/0", Detail: "../calc_test.cc:7: Failure"},
	}
	assertFailures(t, got, want)
}

func TestParseGTestFailures_NoFailures(t *testing.T) {
	out := `[==========] Running 1 test from 1 test suite.
[ RUN      ] CalcTest.Adds
[       OK ] CalcTest.Adds (0 ms)
[==========] 1 test from 1 test suite ran. (0 ms total)
[  PASSED  ] 1 test.`
	if got := parseTestFailures("./calc_test --gtest_filter=*", out); len(got) != 0 {
		t.Errorf("expected no failures, got %v", got)
	}
}

func TestParseRobotFailures(t *testing.T) {
	out := `==============================================================================
Calc :: Arithmetic tests
==============================================================================
Addition Works                                                        | PASS |
------------------------------------------------------------------------------
Subtraction Works                                                     | FAIL |
1 != 2
------------------------------------------------------------------------------
Division Works                                                        | FAIL |
ZeroDivisionError: integer division or modulo by zero
------------------------------------------------------------------------------
Calc :: Arithmetic tests                                              | FAIL |
3 tests, 1 passed, 2 failed
==============================================================================`
	got := parseTestFailures("robot tests/calc.robot", out)
	want := []testFailure{
		{Name: "Subtraction Works", Detail: "1 != 2"},
		{Name: "Division Works", Detail: "ZeroDivisionError: integer division or modulo by zero"},
	}
	assertFailures(t, got, want)
}

func TestParseRobotFailures_MessageLessAndCritical(t *testing.T) {
	// A failure whose next line is a separator (no message) yields no detail, and
	// the older "N critical tests, ..." suite statistics line still suppresses the
	// suite-summary row. The pabot wrapper routes to the same parser.
	out := `==============================================================================
Smoke                                                                 | FAIL |
------------------------------------------------------------------------------
Smoke                                                                 | FAIL |
1 critical test, 0 passed, 1 failed
1 test total, 0 passed, 1 failed
==============================================================================`
	got := parseTestFailures("pabot tests/", out)
	want := []testFailure{
		{Name: "Smoke"},
	}
	assertFailures(t, got, want)
}

func TestParseRobotFailures_NoFailures(t *testing.T) {
	out := `==============================================================================
Calc                                                                  | PASS |
------------------------------------------------------------------------------
Calc                                                                  | PASS |
1 test, 1 passed, 0 failed
==============================================================================`
	if got := parseTestFailures("robot tests/", out); len(got) != 0 {
		t.Errorf("expected no failures, got %v", got)
	}
}

func TestParsePlaywrightFailures(t *testing.T) {
	out := `Running 3 tests using 1 worker

  ✓  1 example.spec.ts:3:1 › has title (1.2s)
  ✘  2 example.spec.ts:7:1 › get started link (0.5s)
  ✘  3 [firefox] › nav.spec.ts:4:1 › navigation works (0.3s)


  1) example.spec.ts:7:1 › get started link ──────────────────────────────────

    Error: expect(received).toHaveTitle(expected)

    Expected pattern: /Playwright/
        at example.spec.ts:8:5

  2) [firefox] › nav.spec.ts:4:1 › navigation works ──────────────────────────

    TimeoutError: locator.click: Timeout 30000ms exceeded.


  2 failed
    example.spec.ts:7:1 › get started link ─────────────────────────────────────
    [firefox] › nav.spec.ts:4:1 › navigation works ─────────────────────────────
  1 passed (2.0s)`
	got := parseTestFailures("npx playwright test", out)
	want := []testFailure{
		{Name: "example.spec.ts:7:1 › get started link", Detail: "Error: expect(received).toHaveTitle(expected)"},
		{Name: "[firefox] › nav.spec.ts:4:1 › navigation works", Detail: "TimeoutError: locator.click: Timeout 30000ms exceeded."},
	}
	assertFailures(t, got, want)
}

func TestParsePlaywrightFailures_NoFailures(t *testing.T) {
	// A clean run prints no numbered "N)" failure headers, so the inline progress
	// lines must not be mistaken for failures.
	out := `Running 1 test using 1 worker

  ✓  1 example.spec.ts:3:1 › has title (1.2s)

  1 passed (2.0s)`
	if got := parseTestFailures("playwright test", out); len(got) != 0 {
		t.Errorf("expected no failures, got %v", got)
	}
}

func TestParseMinitestFailures(t *testing.T) {
	out := `Run options: --seed 4242

# Running:

.F.E

Finished in 0.001234s, 3242.5 runs/s, 1621.2 assertions/s.

  1) Failure:
CalculatorTest#test_addition [test/calculator_test.rb:8]:
Expected: 5
  Actual: 4

  2) Error:
CalculatorTest#test_division:
ZeroDivisionError: divided by 0
    test/calculator_test.rb:12:in ` + "`/'" + `

4 runs, 2 assertions, 1 failures, 1 errors, 0 skips`
	got := parseTestFailures("rails test", out)
	want := []testFailure{
		{Name: "CalculatorTest#test_addition", Detail: "Expected: 5"},
		{Name: "CalculatorTest#test_division", Detail: "ZeroDivisionError: divided by 0"},
	}
	assertFailures(t, got, want)
}

func TestParseMinitestFailures_LocationFallback(t *testing.T) {
	// When a failure block carries no message line beneath the id, the bracketed
	// source location is used as the detail.
	out := `  1) Failure:
WidgetTest#test_renders [test/widget_test.rb:20]:

  2) Failure:
WidgetTest#test_hides [test/widget_test.rb:30]:
Expected false to be truthy.`
	got := parseTestFailures("rake test", out)
	want := []testFailure{
		{Name: "WidgetTest#test_renders", Detail: "test/widget_test.rb:20"},
		{Name: "WidgetTest#test_hides", Detail: "Expected false to be truthy."},
	}
	assertFailures(t, got, want)
}

func TestParseMinitestFailures_NoFailures(t *testing.T) {
	out := `Run options: --seed 1

# Running:

...

Finished in 0.001s, 3000.0 runs/s, 3000.0 assertions/s.

3 runs, 3 assertions, 0 failures, 0 errors, 0 skips`
	if got := parseTestFailures("ruby -Itest test/calc_test.rb", out); len(got) != 0 {
		t.Errorf("expected no failures, got %v", got)
	}
}

func TestParseScalaTestFailures(t *testing.T) {
	out := `[info] CalculatorSpec:
[info] Calculator
[info] - should add two numbers
[info] - should subtract correctly *** FAILED ***
[info]   2 did not equal 3 (CalculatorSpec.scala:15)
[info]   at scala.Predef$.assert(Predef.scala:223)
[info] - should multiply *** FAILED ***
[error]   java.lang.ArithmeticException: / by zero (CalculatorSpec.scala:22)
[info] Run completed in 1 second.
[info] *** 2 TESTS FAILED ***`
	got := parseTestFailures("sbt test", out)
	want := []testFailure{
		{Name: "should subtract correctly", Detail: "2 did not equal 3 (CalculatorSpec.scala:15)"},
		{Name: "should multiply", Detail: "java.lang.ArithmeticException: / by zero (CalculatorSpec.scala:22)"},
	}
	assertFailures(t, got, want)
}

func TestParseScalaTestFailures_NoFailures(t *testing.T) {
	out := `[info] CalculatorSpec:
[info] Calculator
[info] - should add two numbers
[info] - should subtract correctly
[info] Run completed in 1 second.
[info] All tests passed.`
	if got := parseTestFailures("sbt test", out); len(got) != 0 {
		t.Errorf("expected no failures, got %v", got)
	}
}

func TestParseClojureTestFailures(t *testing.T) {
	out := `
lein test myapp.core-test

FAIL in (add-test) (core_test.clj:7)
adds two numbers
expected: (= 4 (add 2 2))
  actual: (not (= 4 5))

ERROR in (div-test) (core_test.clj:12)
Uncaught exception, not in assertion.
expected: nil
  actual: java.lang.ArithmeticException: Divide by zero

Ran 2 tests containing 2 assertions.
2 failures, 0 errors.`
	got := parseTestFailures("lein test", out)
	want := []testFailure{
		{Name: "add-test (core_test.clj:7)", Detail: "(not (= 4 5))"},
		{Name: "div-test (core_test.clj:12)", Detail: "java.lang.ArithmeticException: Divide by zero"},
	}
	assertFailures(t, got, want)
}

func TestParseClojureTestFailures_NoActualDetail(t *testing.T) {
	out := `FAIL in (lonely-test) (core_test.clj:3)

FAIL in (next-test) (core_test.clj:9)
  actual: (not (= 1 2))`
	got := parseTestFailures("clojure -M:test", out)
	want := []testFailure{
		{Name: "lonely-test (core_test.clj:3)"},
		{Name: "next-test (core_test.clj:9)", Detail: "(not (= 1 2))"},
	}
	assertFailures(t, got, want)
}

func TestParseClojureTestFailures_NoFailures(t *testing.T) {
	out := `lein test myapp.core-test

Ran 2 tests containing 2 assertions.
0 failures, 0 errors.`
	if got := parseTestFailures("lein test", out); len(got) != 0 {
		t.Errorf("expected no failures, got %v", got)
	}
}

func TestParseFoundryTestFailures(t *testing.T) {
	out := `
Ran 3 tests for test/Counter.t.sol:CounterTest
[PASS] testFuzz_SetNumber(uint256) (runs: 256, μ: 27567, ~: 28387)
[FAIL: assertion failed: 1 != 2] test_Increment() (gas: 28392)
[FAIL: arithmetic underflow or overflow (0x11)] test_Decrement() (gas: 12044)
Suite result: FAILED. 1 passed; 2 failed; 0 skipped; finished in 1.20ms`
	got := parseTestFailures("forge test", out)
	want := []testFailure{
		{Name: "test_Increment()", Detail: "assertion failed: 1 != 2"},
		{Name: "test_Decrement()", Detail: "arithmetic underflow or overflow (0x11)"},
	}
	assertFailures(t, got, want)
}

func TestParseFoundryTestFailures_LegacyReasonAndBareMarker(t *testing.T) {
	// Older forge builds spell the marker "[FAIL. Reason: ...]", and a setup
	// revert can surface as a bare "[FAIL]" with no reason.
	out := `[FAIL. Reason: Assertion failed] test_Old() (gas: 1234)
[FAIL] test_NoReason() (gas: 5678)`
	got := parseTestFailures("forge test --match-test test_Old", out)
	want := []testFailure{
		{Name: "test_Old()", Detail: "Assertion failed"},
		{Name: "test_NoReason()"},
	}
	assertFailures(t, got, want)
}

func TestParseFoundryTestFailures_Deduplicates(t *testing.T) {
	// A fuzz failure can be reported more than once (e.g. across shrink output);
	// the same test name must not yield duplicate entries.
	out := `[FAIL: assertion failed] testFuzz_Add(uint256) (runs: 12, gas: 100)
[FAIL: assertion failed] testFuzz_Add(uint256) (runs: 1, gas: 100)`
	got := parseTestFailures("forge test", out)
	want := []testFailure{
		{Name: "testFuzz_Add(uint256)", Detail: "assertion failed"},
	}
	assertFailures(t, got, want)
}

func TestParseFoundryTestFailures_NoFailures(t *testing.T) {
	out := `Ran 1 test for test/Counter.t.sol:CounterTest
[PASS] test_Increment() (gas: 28392)
Suite result: ok. 1 passed; 0 failed; 0 skipped; finished in 1.20ms`
	if got := parseTestFailures("forge test", out); len(got) != 0 {
		t.Errorf("expected no failures, got %v", got)
	}
}

func TestParseCucumberFailures(t *testing.T) {
	// Ruby cucumber's "Failing Scenarios:" block: each line is a re-runnable
	// "cucumber <location> # Scenario: <name>" command. The "Scenario:" keyword is
	// stripped so Name is the bare description and Detail the re-runnable location.
	out := `Failing Scenarios:
cucumber features/addition.feature:3 # Scenario: Add two numbers
cucumber features/addition.feature:9 # Scenario Outline: Add many numbers

2 scenarios (2 failed)
6 steps (2 failed, 4 passed)`
	got := parseTestFailures("bundle exec cucumber", out)
	want := []testFailure{
		{Name: "Add two numbers", Detail: "features/addition.feature:3"},
		{Name: "Add many numbers", Detail: "features/addition.feature:9"},
	}
	assertFailures(t, got, want)
}

func TestParseCucumberFailures_JSNoDescription(t *testing.T) {
	// cucumber-js's rerun lines carry only the location, no "# Scenario: <name>"
	// suffix, so the location becomes the name and the detail is empty.
	out := `Failures:

1) Scenario: Subtract numbers # features/sub.feature:4
   ✖ Then the result should be 1
       AssertionError: expected 2 to equal 1

Failing scenarios:
cucumber-js features/sub.feature:4

1 scenario (1 failed)`
	got := parseTestFailures("npx cucumber-js", out)
	want := []testFailure{
		{Name: "features/sub.feature:4"},
	}
	assertFailures(t, got, want)
}

func TestParseCucumberFailures_None(t *testing.T) {
	// A passing run prints no "cucumber <file>.feature:<line>" rerun lines, so
	// nothing is extracted.
	out := `2 scenarios (2 passed)
6 steps (6 passed)`
	if got := parseTestFailures("cucumber", out); len(got) != 0 {
		t.Errorf("expected no failures, got %v", got)
	}
}

func TestParseBehaveFailures(t *testing.T) {
	// behave (Python Gherkin BDD) closes a failing run with a "Failing scenarios:"
	// block of indented "<file>.feature:<line>  <scenario name>" entries. Name is
	// the scenario name and Detail the re-runnable location. The verbose run's
	// per-scenario "Scenario: <name>  # <file>:<line>" trace lines printed earlier
	// must not be mistaken for failures.
	out := `Feature: Calculator # features/calc.feature:1

  Scenario: Add two numbers  # features/calc.feature:5
    Given I have entered 50  # features/steps/calc.py:3
    Then the result should be 99  # features/steps/calc.py:9
      Assertion Failed: 70 != 99

Failing scenarios:
  features/calc.feature:5  Add two numbers
  features/calc.feature:12  Subtract two numbers

0 features passed, 1 failed, 0 skipped
1 scenario passed, 2 failed, 0 skipped`
	got := parseTestFailures("python -m behave", out)
	want := []testFailure{
		{Name: "Add two numbers", Detail: "features/calc.feature:5"},
		{Name: "Subtract two numbers", Detail: "features/calc.feature:12"},
	}
	assertFailures(t, got, want)
}

func TestParseBehaveFailures_None(t *testing.T) {
	// A passing run prints no "Failing scenarios:" block, so nothing is extracted —
	// and the per-scenario trace lines outside that block are never matched.
	out := `Feature: Calculator # features/calc.feature:1

  Scenario: Add two numbers  # features/calc.feature:5
    Then the result should be 99  # features/steps/calc.py:9

1 feature passed, 0 failed, 0 skipped
1 scenario passed, 0 failed, 0 skipped`
	if got := parseTestFailures("behave", out); len(got) != 0 {
		t.Errorf("expected no failures, got %v", got)
	}
}

func TestParseBehaveFailures_NotMisbehave(t *testing.T) {
	// "\bbehave\b" must not fire on prose like "misbehave", so a command that only
	// mentions it in passing is not classified as a behave run.
	out := `Failing scenarios:
  features/calc.feature:5  Add two numbers`
	if got := parseTestFailures("echo the tests misbehave", out); len(got) != 0 {
		t.Errorf("expected no failures for non-behave command, got %v", got)
	}
}

func TestParseKarmaFailures(t *testing.T) {
	// Karma's progress reporter prints "<browser> <suite> <test> FAILED" per
	// failing spec, with the assertion on the tab-indented line beneath. The
	// browser prefix ("<name> <version> (<platform>)") is stripped from Name, and
	// the run-summary "Executed N of M (X FAILED)" line — whose FAILED is inside
	// parentheses — is not mistaken for a failure.
	out := `HeadlessChrome 120.0.0 (Linux x86_64): Executed 1 of 3 SUCCESS (0 secs / 0.1 secs)
HeadlessChrome 120.0.0 (Linux x86_64) AppComponent should create the app FAILED
	Expected undefined to be truthy.
	    at <Jasmine>
HeadlessChrome 120.0.0 (Linux x86_64) MathService adds numbers FAILED
	Expected 5 to equal 4.
	    at UserContext.<anonymous> (src/math.service.spec.ts:12:21)
HeadlessChrome 120.0.0 (Linux x86_64): Executed 3 of 3 (2 FAILED) (0.2 secs / 0.15 secs)`
	got := parseTestFailures("ng test --watch=false", out)
	want := []testFailure{
		{Name: "AppComponent should create the app", Detail: "Expected undefined to be truthy."},
		{Name: "MathService adds numbers", Detail: "Expected 5 to equal 4."},
	}
	assertFailures(t, got, want)
}

func TestParseKarmaFailures_DedupesAcrossBrowsers(t *testing.T) {
	// The same spec failing in two browsers emits two "<browser> ... FAILED" lines
	// sharing one description; it is reported once.
	out := `Chrome 120.0.0 (Linux x86_64) Widget renders FAILED
	Error: boom
Firefox 121.0.0 (Linux x86_64) Widget renders FAILED
	Error: boom`
	got := parseTestFailures("karma start", out)
	want := []testFailure{
		{Name: "Widget renders", Detail: "Error: boom"},
	}
	assertFailures(t, got, want)
}

func TestParseKarmaFailures_None(t *testing.T) {
	// A passing run prints only the SUCCESS summary, so nothing is extracted.
	out := `HeadlessChrome 120.0.0 (Linux x86_64): Executed 3 of 3 SUCCESS (0.2 secs / 0.15 secs)
TOTAL: 3 SUCCESS`
	if got := parseTestFailures("ng test", out); len(got) != 0 {
		t.Errorf("expected no failures, got %v", got)
	}
}

func TestParseGinkgoFailures(t *testing.T) {
	out := `Running Suite: Books Suite
=========================

• [FAILED] [0.001 seconds]
Book Categorizing book length
  [It] should be a short book

  Expected
      <int>: 5
  to equal
      <int>: 6

------------------------------

Summarizing 2 Failures:

  [FAIL] Book Categorizing book length [It] should be a short book
  /home/me/books/book_test.go:54
  [PANIC!] Book Loading from disk [It] reads the file
  /home/me/books/loader_test.go:20

Ran 4 of 4 Specs in 0.003 seconds
FAIL! -- 2 Passed | 2 Failed | 0 Pending | 0 Skipped`
	got := parseTestFailures("ginkgo -r", out)
	want := []testFailure{
		{Name: "Book Categorizing book length [It] should be a short book", Detail: "/home/me/books/book_test.go:54"},
		{Name: "Book Loading from disk [It] reads the file", Detail: "/home/me/books/loader_test.go:20"},
	}
	assertFailures(t, got, want)
}

func TestParseGinkgoFailures_NoSummaryBlock(t *testing.T) {
	// Without the "Summarizing N Failures" block (e.g. a passing run or
	// non-Ginkgo output), nothing is extracted — the inline "• [FAILED]" markers
	// alone are not parsed.
	out := `Running Suite: Books Suite
Ran 4 of 4 Specs in 0.003 seconds
SUCCESS! -- 4 Passed | 0 Failed | 0 Pending | 0 Skipped`
	if got := parseTestFailures("ginkgo", out); len(got) != 0 {
		t.Errorf("expected no failures, got %v", got)
	}
}

func TestParseGinkgoFailures_Deduplicates(t *testing.T) {
	// A flaky spec retried under --flake-attempts can appear twice in the summary;
	// the same spec text must not yield duplicate entries.
	out := `Summarizing 1 Failure:

  [FAIL] Widget renders [It] draws a border
  /app/widget_test.go:12
  [FAIL] Widget renders [It] draws a border
  /app/widget_test.go:12`
	got := parseTestFailures("ginkgo run", out)
	want := []testFailure{
		{Name: "Widget renders [It] draws a border", Detail: "/app/widget_test.go:12"},
	}
	assertFailures(t, got, want)
}

func TestParseJasmineFailures(t *testing.T) {
	out := `Randomized with seed 12345
Started
F.F


Failures:
1) A calculator adds two numbers
  Message:
    Expected 3 to be 4.
  Stack:
    Error: Expected 3 to be 4.
        at <Jasmine>
        at UserContext.<anonymous> (spec/calcSpec.js:5:21)

2) A calculator throws on divide by zero
  Message:
    Expected function to throw an Error.
  Stack:
    Error: Expected function to throw an Error.
        at UserContext.<anonymous> (spec/calcSpec.js:12:30)

3 specs, 2 failures
Finished in 0.012 seconds`
	got := parseTestFailures("npx jasmine", out)
	want := []testFailure{
		{Name: "A calculator adds two numbers", Detail: "Expected 3 to be 4."},
		{Name: "A calculator throws on divide by zero", Detail: "Expected function to throw an Error."},
	}
	assertFailures(t, got, want)
}

func TestParseJasmineFailures_IgnoresPendingSection(t *testing.T) {
	// Jasmine lists pending specs in their own "Pending:" block using the same
	// "N) <name>" numbering as failures; only the "Failures:" block is parsed, and
	// the scan stops at the "Pending:" header so a pending spec is never counted.
	out := `Failures:
1) Widget renders a border
  Message:
    Expected '' to be 'solid'.
  Stack:
    Error: ...

Pending:
1) Widget animates
  Temporarily disabled with xit

2 specs, 1 failure, 1 pending spec`
	got := parseTestFailures("jasmine", out)
	want := []testFailure{
		{Name: "Widget renders a border", Detail: "Expected '' to be 'solid'."},
	}
	assertFailures(t, got, want)
}

func TestParseJasmineFailures_NoFailures(t *testing.T) {
	// A passing run prints no "Failures:" block, so nothing is extracted.
	out := `Started
...

3 specs, 0 failures
Finished in 0.004 seconds`
	if got := parseTestFailures("jasmine", out); len(got) != 0 {
		t.Errorf("expected no failures, got %v", got)
	}
}

func TestParsePesterFailures(t *testing.T) {
	out := `Describing Get-Planet
  [+] Returns Earth 12ms (10ms|2ms)
  [-] Returns Mars 8ms (7ms|1ms)
   Expected 'Mars', but got 'Earth'.
   at $expected | Should -Be $actual, /tests/Get-Planet.Tests.ps1:18
  [-] Throws on bad input 5ms
   Expected an exception to be thrown, but none was.
   at /tests/Get-Planet.Tests.ps1:24
Tests completed in 1.2s
Tests Passed: 1, Failed: 2, Skipped: 0`
	got := parseTestFailures("Invoke-Pester", out)
	want := []testFailure{
		{Name: "Returns Mars", Detail: "Expected 'Mars', but got 'Earth'."},
		{Name: "Throws on bad input", Detail: "Expected an exception to be thrown, but none was."},
	}
	assertFailures(t, got, want)
}

func TestParsePesterFailures_StopsAtNextBlock(t *testing.T) {
	// A failure whose body is empty must not borrow the next test's message: the
	// detail scan stops at the next result marker or a "Describing"/"Context"
	// header.
	out := `Describing Math
  [-] adds 3ms
Describing Strings
  [-] upcases 2ms
   Expected 'FOO', but got 'foo'.`
	got := parseTestFailures("pester", out)
	want := []testFailure{
		{Name: "adds"},
		{Name: "upcases", Detail: "Expected 'FOO', but got 'foo'."},
	}
	assertFailures(t, got, want)
}

func TestParsePesterFailures_NoFailures(t *testing.T) {
	// An all-passing run prints only "[+]" markers, so nothing is extracted.
	out := `Describing Get-Planet
  [+] Returns Earth 12ms
  [!] Pending case 0ms
Tests Passed: 1, Failed: 0, Skipped: 1`
	if got := parseTestFailures("Invoke-Pester", out); len(got) != 0 {
		t.Errorf("expected no failures, got %v", got)
	}
}

func assertFailures(t *testing.T, got, want []testFailure) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("failure count = %d, want %d\n got %v\nwant %v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("failure[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}
