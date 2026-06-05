package tools

import (
	"fmt"
	"strings"
	"testing"
)

func TestClassifyTestRunner(t *testing.T) {
	cases := map[string]testRunner{
		"go test ./...":                         runnerGo,
		"GOFLAGS=-count=1 go test -run X ./p":   runnerGo,
		"pytest -q tests/":                      runnerPytest,
		"python -m py.test":                     runnerPytest,
		"python -m unittest test_mod":           runnerUnittest,
		"python -m unittest discover":           runnerUnittest,
		"nose2":                                 runnerUnittest,
		"nose2 -v tests":                        runnerUnittest,
		"python -m nose2":                       runnerUnittest,
		"nosetests tests/":                      runnerUnittest,
		"echo a diagnosis2 of the problem":      runnerNone,
		"npm test":                              runnerJest,
		"npm run test -- --ci":                  runnerJest,
		"yarn test":                             runnerJest,
		"pnpm test":                             runnerJest,
		"npx vitest run":                        runnerJest,
		"npx jest src/":                         runnerJest,
		"cargo test":                            runnerCargo,
		"cargo test --release foo":              runnerCargo,
		"cargo nextest run":                     runnerNextest,
		"cargo nextest run --no-fail-fast":      runnerNextest,
		"rspec":                                 runnerRSpec,
		"bundle exec rspec spec/foo_spec.rb":    runnerRSpec,
		"bin/rspec":                             runnerRSpec,
		"vendor/bin/phpunit":                    runnerPHPUnit,
		"phpunit --filter testFoo":              runnerPHPUnit,
		"dotnet test":                           runnerDotnet,
		"dotnet test ./MyApp.sln -v normal":     runnerDotnet,
		"mvn test":                              runnerMaven,
		"mvn -q verify -Dtest=FooTest":          runnerMaven,
		"./mvnw test":                           runnerMaven,
		"gradle test":                           runnerGradle,
		"./gradlew test --tests FooTest":        runnerGradle,
		"gradlew check":                         runnerGradle,
		"mix test":                              runnerExUnit,
		"mix test test/foo_test.exs:12":         runnerExUnit,
		"node --test":                           runnerTAP,
		"node --test test/*.js":                 runnerTAP,
		"npx tape test/*.js":                    runnerTAP,
		"bats test/":                            runnerTAP,
		"bats test.bats":                        runnerTAP,
		"npx bats tests/":                       runnerTAP,
		"echo acrobats perform":                 runnerNone,
		"deno test":                             runnerDeno,
		"deno test --allow-read mod_test.ts":    runnerDeno,
		"swift test":                            runnerSwift,
		"swift test --filter CalculatorTests":   runnerSwift,
		"bun test":                              runnerBun,
		"bun test ./math.test.ts":               runnerBun,
		"bun run test":                          runnerNone,
		"mocha":                                 runnerMocha,
		"npx mocha test/*.js":                   runnerMocha,
		"ctest":                                 runnerCTest,
		"ctest -R '^Foo$' --output-on-failure":  runnerCTest,
		"cmake --build . && ctest":              runnerCTest,
		"playwright test":                       runnerPlaywright,
		"npx playwright test e2e/login.spec.ts": runnerPlaywright,
		"rails test":                            runnerMinitest,
		"rails test test/models/user_test.rb":   runnerMinitest,
		"rake test":                             runnerMinitest,
		"bundle exec rake test TEST=test/x.rb":  runnerMinitest,
		"ruby -Itest test/calculator_test.rb":   runnerMinitest,
		"dart test":                             runnerDart,
		"dart test test/calc_test.dart":         runnerDart,
		"flutter test":                          runnerDart,
		"flutter test test/widget_test.dart":    runnerDart,
		"julia test/runtests.jl":                runnerJulia,
		"julia --project=. test/runtests.jl":    runnerJulia,
		"julia -e 'using Pkg; Pkg.test()'":      runnerJulia,
		"julia script.jl":                       runnerNone,
		"sbt test":                              runnerScala,
		"sbt 'testOnly *CalculatorSpec'":        runnerScala,
		"./sbt test":                            runnerScala,
		"echo subtle differences":               runnerNone,
		"echo rake testing notes":               runnerNone,
		"ls -la":                                runnerNone,
		"echo go testing the waters":            runnerNone,
		"echo rspecs are great":                 runnerNone,
		"echo plan the upgrade":                 runnerNone,
		"echo swift testing guide":              runnerNone,
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

func TestParseMochaFailures_NoFailures(t *testing.T) {
	out := `  Array
    ✓ works

  1 passing (3ms)
`
	if got := parseTestFailures("mocha", out); len(got) != 0 {
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
