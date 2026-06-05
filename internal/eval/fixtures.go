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
