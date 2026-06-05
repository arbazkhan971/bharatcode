package tools

import "testing"

func TestClassifyTestRunner(t *testing.T) {
	cases := map[string]testRunner{
		"go test ./...":                       runnerGo,
		"GOFLAGS=-count=1 go test -run X ./p": runnerGo,
		"pytest -q tests/":                    runnerPytest,
		"python -m py.test":                   runnerPytest,
		"python -m unittest test_mod":         runnerUnittest,
		"python -m unittest discover":         runnerUnittest,
		"npm test":                            runnerJest,
		"npm run test -- --ci":                runnerJest,
		"yarn test":                           runnerJest,
		"pnpm test":                           runnerJest,
		"npx vitest run":                      runnerJest,
		"npx jest src/":                       runnerJest,
		"cargo test":                          runnerCargo,
		"cargo test --release foo":            runnerCargo,
		"rspec":                               runnerRSpec,
		"bundle exec rspec spec/foo_spec.rb":  runnerRSpec,
		"bin/rspec":                           runnerRSpec,
		"vendor/bin/phpunit":                  runnerPHPUnit,
		"phpunit --filter testFoo":            runnerPHPUnit,
		"dotnet test":                         runnerDotnet,
		"dotnet test ./MyApp.sln -v normal":   runnerDotnet,
		"ls -la":                              runnerNone,
		"echo go testing the waters":          runnerNone,
		"echo rspecs are great":               runnerNone,
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

func TestParseTestFailures_NonTestCommandIgnored(t *testing.T) {
	// Output contains FAIL/FAILED words but the command is not a test runner.
	out := "FAILED to connect\n--- FAIL: not a test"
	if got := parseTestFailures("curl http://x", out); got != nil {
		t.Errorf("expected nil for non-test command, got %v", got)
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
