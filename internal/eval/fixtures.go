package eval

import (
	"fmt"
	"os"
	"path/filepath"
)

// writeFile creates path (relative to dir) with content, creating parent
// directories as needed.
func writeFile(dir, path, content string) error {
	full := filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return fmt.Errorf("eval fixture: mkdir %s: %w", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		return fmt.Errorf("eval fixture: write %s: %w", path, err)
	}
	return nil
}

// writeGoMod writes a minimal go.mod so go build works in the fixture dir.
func writeGoMod(dir, module string) error {
	return writeFile(dir, "go.mod", "module "+module+"\n\ngo 1.21\n")
}

// fixtureSyntaxError creates a repo with a Go file that is missing an import.
func fixtureSyntaxError(dir string) error {
	if err := writeGoMod(dir, "example/syntaxfix"); err != nil {
		return err
	}
	return writeFile(dir, "main.go",
		// Missing import "fmt" — the file references fmt.Println.
		`package main

func main() {
	fmt.Println("hello")
}
`)
}

// fixtureMissingFunc creates a repo where util.go calls an undefined helper().
func fixtureMissingFunc(dir string) error {
	if err := writeGoMod(dir, "example/missingfunc"); err != nil {
		return err
	}
	return writeFile(dir, "util.go",
		`package main

func main() {
	helper()
}
`)
}

// fixtureUpdateComment creates a repo with an outdated comment.
func fixtureUpdateComment(dir string) error {
	if err := writeGoMod(dir, "example/updatecomment"); err != nil {
		return err
	}
	return writeFile(dir, "README.go",
		`package main

// oldComment describes the old function signature.
func Process(x int) {}
`)
}

// fixtureAddReturn creates a repo with a function missing its return value.
func fixtureAddReturn(dir string) error {
	if err := writeGoMod(dir, "example/addreturn"); err != nil {
		return err
	}
	return writeFile(dir, "calc.go",
		`package main

func add(a, b int) {
	_ = a + b
}
`)
}

// fixtureOffByOne creates a repo with an off-by-one index access.
func fixtureOffByOne(dir string) error {
	if err := writeGoMod(dir, "example/offbyone"); err != nil {
		return err
	}
	return writeFile(dir, "slice.go",
		`package main

func last(s []int) int {
	n := len(s)
	return s[n]
}
`)
}

// -------- codex-parity fixtures --------
//
// These seed small, recurring scaffolds the agent is asked to build or repair.
// The eval tools are hermetic (no real file I/O), so a fixture only needs to
// establish enough starting state for the task prompt to make sense; the
// parity metrics are derived from the scripted edits and verification calls.

// fixtureTodoApp seeds an empty project skeleton for a todo CLI app.
func fixtureTodoApp(dir string) error {
	if err := writeGoMod(dir, "example/todoapp"); err != nil {
		return err
	}
	return writeFile(dir, "main.go",
		`package main

func main() {}
`)
}

// fixtureCalculator seeds an empty project skeleton for a calculator.
func fixtureCalculator(dir string) error {
	if err := writeGoMod(dir, "example/calculator"); err != nil {
		return err
	}
	return writeFile(dir, "main.go",
		`package main

func main() {}
`)
}

// fixtureNotesApp seeds an empty project skeleton for a notes app.
func fixtureNotesApp(dir string) error {
	if err := writeGoMod(dir, "example/notesapp"); err != nil {
		return err
	}
	return writeFile(dir, "main.go",
		`package main

func main() {}
`)
}

// fixtureQuizApp seeds an empty project skeleton for a quiz app.
func fixtureQuizApp(dir string) error {
	if err := writeGoMod(dir, "example/quizapp"); err != nil {
		return err
	}
	return writeFile(dir, "main.go",
		`package main

func main() {}
`)
}

// fixtureGoBug seeds a Go package with a failing sum() helper to repair.
func fixtureGoBug(dir string) error {
	if err := writeGoMod(dir, "example/gobug"); err != nil {
		return err
	}
	if err := writeFile(dir, "sum.go",
		`package gobug

// sum should add a and b, but the operator is wrong.
func sum(a, b int) int {
	return a - b
}
`); err != nil {
		return err
	}
	return writeFile(dir, "sum_test.go",
		`package gobug

import "testing"

func TestSum(t *testing.T) {
	if sum(2, 3) != 5 {
		t.Fatalf("sum(2,3) = %d, want 5", sum(2, 3))
	}
}
`)
}

// fixtureNodeBug seeds a small Node project with a failing test to repair.
func fixtureNodeBug(dir string) error {
	if err := writeFile(dir, "package.json",
		`{
  "name": "node-bug",
  "version": "1.0.0",
  "scripts": { "test": "node --test" }
}
`); err != nil {
		return err
	}
	if err := writeFile(dir, "sum.js",
		`function sum(a, b) {
  return a - b;
}

module.exports = { sum };
`); err != nil {
		return err
	}
	return writeFile(dir, "sum.test.js",
		`const test = require("node:test");
const assert = require("node:assert");
const { sum } = require("./sum");

test("sum adds two numbers", () => {
  assert.strictEqual(sum(2, 3), 5);
});
`)
}

// fixtureFrontendBuild seeds a small frontend project whose build the agent is
// asked to repair and then verify with the build command.
func fixtureFrontendBuild(dir string) error {
	if err := writeFile(dir, "package.json",
		`{
  "name": "frontend-build",
  "version": "1.0.0",
  "scripts": { "build": "vite build" }
}
`); err != nil {
		return err
	}
	return writeFile(dir, "src/main.js",
		`import { greeting } from "./missing";

document.body.textContent = greeting;
`)
}
