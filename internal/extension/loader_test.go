package extension

import (
	"os"
	"path/filepath"
	"testing"
)

// writeExtension creates a directory extension under root/name with the given
// manifest JSON and optional command body files (keyed by filename under
// commands/).
func writeExtension(t *testing.T, root, name, manifestJSON string, bodies map[string]string) {
	t.Helper()
	extDir := filepath.Join(root, name)
	if err := os.MkdirAll(extDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", extDir, err)
	}
	if err := os.WriteFile(filepath.Join(extDir, manifestName), []byte(manifestJSON), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if len(bodies) > 0 {
		cmdDir := filepath.Join(extDir, "commands")
		if err := os.MkdirAll(cmdDir, 0o755); err != nil {
			t.Fatalf("mkdir commands: %v", err)
		}
		for file, body := range bodies {
			if err := os.WriteFile(filepath.Join(cmdDir, file), []byte(body), 0o644); err != nil {
				t.Fatalf("write body %s: %v", file, err)
			}
		}
	}
}

func TestLoadDirectoryExtensionInlinePrompt(t *testing.T) {
	dir := t.TempDir()
	writeExtension(t, dir, "hello", `{
		"name": "hello-ext",
		"description": "greeter",
		"commands": [{"name": "hello", "description": "say hi", "prompt": "Say hello"}]
	}`, nil)

	host, err := Load(Options{UserDir: dir})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cmd, ok := findCommand(host, "hello")
	if !ok {
		t.Fatalf("command hello not loaded; got %v", host.GetCommands())
	}
	if cmd.Prompt != "Say hello" || cmd.Source != "hello-ext" {
		t.Fatalf("command = %+v want prompt 'Say hello' source 'hello-ext'", cmd)
	}
}

func TestLoadDirectoryExtensionPromptFile(t *testing.T) {
	dir := t.TempDir()
	writeExtension(t, dir, "review", `{
		"name": "review-ext",
		"commands": [{"name": "review", "description": "review code"}]
	}`, map[string]string{"review.md": "Review the diff carefully."})

	host, err := Load(Options{UserDir: dir})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cmd, ok := findCommand(host, "review")
	if !ok {
		t.Fatalf("command review not loaded")
	}
	if cmd.Prompt != "Review the diff carefully." {
		t.Fatalf("command body = %q want file contents", cmd.Prompt)
	}
}

// TestProjectOverridesUser confirms a project command shadows a same-named user
// command because the project directory is scanned second.
func TestProjectOverridesUser(t *testing.T) {
	userDir := t.TempDir()
	projDir := t.TempDir()
	writeExtension(t, userDir, "shared", `{
		"name": "user-shared",
		"commands": [{"name": "deploy", "prompt": "user deploy"}]
	}`, nil)
	writeExtension(t, projDir, "shared", `{
		"name": "project-shared",
		"commands": [{"name": "deploy", "prompt": "project deploy"}]
	}`, nil)

	host, err := Load(Options{UserDir: userDir, ProjectDir: projDir})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cmd, ok := findCommand(host, "deploy")
	if !ok {
		t.Fatalf("command deploy not loaded")
	}
	if cmd.Prompt != "project deploy" {
		t.Fatalf("deploy prompt = %q want 'project deploy' (project must win)", cmd.Prompt)
	}
}

// TestLoadSkipsBadManifest confirms a malformed manifest is skipped, not fatal,
// and a well-formed sibling still loads.
func TestLoadSkipsBadManifest(t *testing.T) {
	dir := t.TempDir()
	writeExtension(t, dir, "broken", `{ not valid json`, nil)
	writeExtension(t, dir, "good", `{
		"name": "good-ext",
		"commands": [{"name": "ok", "prompt": "fine"}]
	}`, nil)

	host, err := Load(Options{UserDir: dir})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := findCommand(host, "ok"); !ok {
		t.Fatalf("good extension did not load alongside a broken one")
	}
}

// TestLoadIgnoresDirWithoutManifest confirms a subdirectory without a manifest
// is silently ignored.
func TestLoadIgnoresDirWithoutManifest(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "notanext"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	host, err := Load(Options{UserDir: dir})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, name := range host.Names() {
		if name == "notanext" {
			t.Fatalf("a directory without a manifest was treated as an extension")
		}
	}
}

func TestLoadMissingDirIsNoError(t *testing.T) {
	// A missing directory must not error and must not contribute any directory
	// command. (Commands from compiled extensions registered elsewhere in the
	// package's tests may still be present via the global registry, so this
	// asserts the absence of a directory-sourced command rather than a zero count.)
	host, err := Load(Options{UserDir: filepath.Join(t.TempDir(), "does-not-exist")})
	if err != nil {
		t.Fatalf("Load with missing dir: %v", err)
	}
	for _, c := range host.GetCommands() {
		if c.Source != "" && c.Source != "stub-ext-test" {
			t.Fatalf("unexpected directory command %q from source %q", c.Name, c.Source)
		}
	}
}

func TestResolveCommandBodyRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	_, err := resolveCommandBody(dir, ManifestCommand{Name: "x", PromptFile: "../escape.md"})
	if err == nil {
		t.Fatalf("resolveCommandBody: expected traversal rejection")
	}
}

func TestProjectDirAndUserDirPaths(t *testing.T) {
	if got := ProjectDir("/repo"); got != filepath.Join("/repo", ".bharat", "extensions") {
		t.Fatalf("ProjectDir = %q", got)
	}
	if ProjectDir("") != "" {
		t.Fatalf("ProjectDir(empty) must be empty")
	}
}

func findCommand(h *Host, name string) (Command, bool) {
	for _, c := range h.GetCommands() {
		if c.Name == name {
			return c, true
		}
	}
	return Command{}, false
}
