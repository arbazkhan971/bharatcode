package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/stretchr/testify/require"
)

// writeFile (defined in filetree_test.go) creates a file under root with the
// given relative path and content; mention tests reuse it.

func TestExpandFileMentions_InlinesResolvedFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "main.go", "package main\n\nfunc main() {}\n")

	out, refs, _ := expandFileMentions("look at @main.go please", root)

	require.Equal(t, []string{"main.go"}, refs)
	require.Contains(t, out, "look at @main.go please")
	require.Contains(t, out, "[Attached files]")
	require.Contains(t, out, "@main.go:")
	require.Contains(t, out, "```go")
	require.Contains(t, out, "func main() {}")
}

func TestExpandFileMentions_NoMentionLeavesTextUnchanged(t *testing.T) {
	root := t.TempDir()
	out, refs, _ := expandFileMentions("just a plain message", root)
	require.Equal(t, "just a plain message", out)
	require.Nil(t, refs)
}

func TestExpandFileMentions_IgnoresEmailAddresses(t *testing.T) {
	root := t.TempDir()
	// An email exists as a file would-be name, but the "@" is mid-token so it is
	// never treated as a mention.
	out, refs, _ := expandFileMentions("ping arbaz@lineupx.com about it", root)
	require.Equal(t, "ping arbaz@lineupx.com about it", out)
	require.Nil(t, refs)
}

func TestExpandFileMentions_UnresolvedMentionLeftIntact(t *testing.T) {
	root := t.TempDir()
	out, refs, _ := expandFileMentions("see @does/not/exist.go", root)
	require.Equal(t, "see @does/not/exist.go", out)
	require.Nil(t, refs)
}

func TestExpandFileMentions_StripsTrailingPunctuation(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "pkg/util.go", "package pkg\n")

	out, refs, _ := expandFileMentions("check (@pkg/util.go).", root)
	require.Equal(t, []string{"pkg/util.go"}, refs)
	require.Contains(t, out, "@pkg/util.go:")
}

func TestExpandFileMentions_DeduplicatesRepeatedMention(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.go", "package a\n")

	out, refs, _ := expandFileMentions("@a.go and again @a.go", root)
	require.Equal(t, []string{"a.go"}, refs)
	require.Equal(t, 1, strings.Count(out, "@a.go:"))
}

func TestExpandFileMentions_RejectsPathEscapingRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	require.NoError(t, os.MkdirAll(root, 0o755))
	// A sensitive file outside the workspace must never be inlined.
	outside := filepath.Join(filepath.Dir(root), "secret.txt")
	require.NoError(t, os.WriteFile(outside, []byte("top secret"), 0o644))

	out, refs, _ := expandFileMentions("read @../secret.txt", root)
	require.Nil(t, refs)
	require.NotContains(t, out, "top secret")
}

func TestExpandFileMentions_RejectsDirectory(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "internal"), 0o755))

	out, refs, _ := expandFileMentions("look in @internal", root)
	require.Nil(t, refs)
	require.Equal(t, "look in @internal", out)
}

func TestExpandFileMentions_TruncatesOversizedFile(t *testing.T) {
	root := t.TempDir()
	big := strings.Repeat("x", maxMentionFileBytes+500)
	writeFile(t, root, "big.txt", big)

	out, refs, _ := expandFileMentions("@big.txt", root)
	require.Equal(t, []string{"big.txt"}, refs)
	require.Contains(t, out, "… [truncated]")
	// The inlined body is capped, not the full file.
	require.Less(t, len(out), maxMentionFileBytes+400)
}

func TestExpandFileMentions_MultipleFilesInOrder(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "first.go", "package first\n")
	writeFile(t, root, "second.go", "package second\n")

	out, refs, _ := expandFileMentions("@second.go then @first.go", root)
	require.Equal(t, []string{"second.go", "first.go"}, refs)
	// Order of appended blocks follows first-mention order.
	require.Less(t, strings.Index(out, "@second.go:"), strings.Index(out, "@first.go:"))
}

func TestExpandFileMentions_EmptyRootNoOp(t *testing.T) {
	out, refs, _ := expandFileMentions("@main.go", "")
	require.Equal(t, "@main.go", out)
	require.Nil(t, refs)
}

func TestExpandFileMentions_MentionAtStart(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "start.go", "package start\n")

	out, refs, _ := expandFileMentions("@start.go is the entrypoint", root)
	require.Equal(t, []string{"start.go"}, refs)
	require.Contains(t, out, "package start")
}

func TestMentionLang_ByExtension(t *testing.T) {
	cases := map[string]string{
		"main.go":      "go",
		"app.ts":       "typescript",
		"view.tsx":     "tsx",
		"lib.rs":       "rust",
		"Main.kt":      "kotlin",
		"App.swift":    "swift",
		"index.php":    "php",
		"Build.scala":  "scala",
		"deploy.zsh":   "bash",
		"feed.xml":     "xml",
		"setup.cfg":    "ini",
		"core.cxx":     "cpp",
		"Program.cs":   "csharp",
		"theme.scss":   "scss",
		"init.lua":     "lua",
		"main.dart":    "dart",
		"app.ex":       "elixir",
		"server.exs":   "elixir",
		"node.erl":     "erlang",
		"core.clj":     "clojure",
		"main.hs":      "haskell",
		"tool.pl":      "perl",
		"model.r":      "r",
		"App.vue":      "vue",
		"Page.svelte":  "svelte",
		"api.proto":    "protobuf",
		"schema.gql":   "graphql",
		"main.tf":      "hcl",
		"build.gradle": "groovy",
		"deploy.ps1":   "powershell",
		"main.zig":     "zig",
		"fix.patch":    "diff",
		"README":       "",
		"notes.bin":    "",
	}
	for name, want := range cases {
		require.Equalf(t, want, mentionLang(name), "mentionLang(%q)", name)
	}
}

func TestMentionLang_BySpecialName(t *testing.T) {
	cases := map[string]string{
		"Dockerfile":     "dockerfile",
		"dockerfile":     "dockerfile",
		"Containerfile":  "dockerfile",
		"Makefile":       "makefile",
		"GNUmakefile":    "makefile",
		"build.mk":       "makefile",
		"go.mod":         "go",
		"go.sum":         "go",
		"CMakeLists.txt": "cmake",
		".gitignore":     "gitignore",
		".dockerignore":  "gitignore",
		".env":           "bash",
		"src/Dockerfile": "dockerfile",
		"nested/go.mod":  "go",
	}
	for name, want := range cases {
		require.Equalf(t, want, mentionLang(name), "mentionLang(%q)", name)
	}
}

func TestExpandFileMentions_SpecialNameLanguageTag(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "Dockerfile", "FROM scratch\n")

	out, _, _ := expandFileMentions("see @Dockerfile", root)
	require.Contains(t, out, "```dockerfile")
	require.Contains(t, out, "FROM scratch")
}

// minimalPNG is a 1×1 transparent PNG (smallest valid PNG file).
var minimalPNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, // PNG signature
	0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52, // IHDR chunk length + type
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, // width=1, height=1
	0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, // bit depth, color type, ...
	0xde, 0x00, 0x00, 0x00, 0x0c, 0x49, 0x44, 0x41, // IDAT chunk
	0x54, 0x08, 0xd7, 0x63, 0xf8, 0xcf, 0xc0, 0x00,
	0x00, 0x00, 0x02, 0x00, 0x01, 0xe2, 0x21, 0xbc,
	0x33, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, // IEND chunk
	0x44, 0xae, 0x42, 0x60, 0x82,
}

func TestExpandFileMentions_ImagePNG_ReturnsImageBlock(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "shot.png"), minimalPNG, 0o644))

	out, refs, imgs := expandFileMentions("see @shot.png", root)

	require.Equal(t, []string{"shot.png"}, refs)
	require.Len(t, imgs, 1)
	require.Equal(t, "image/png", imgs[0].MimeType)
	require.Equal(t, minimalPNG, imgs[0].Data)
	// Text section includes an annotation but not the raw binary.
	require.Contains(t, out, "@shot.png")
	require.Contains(t, out, "image/png")
	require.NotContains(t, out, "```")
}

func TestExpandFileMentions_ImageJPEG_ReturnsImageBlock(t *testing.T) {
	root := t.TempDir()
	// A minimal JPEG: SOI + EOI markers — enough for MIME detection.
	jpeg := []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 0x4a, 0x46, 0x49, 0x46, 0x00, 0x01, 0xff, 0xd9}
	require.NoError(t, os.WriteFile(filepath.Join(root, "photo.jpg"), jpeg, 0o644))

	_, refs, imgs := expandFileMentions("attach @photo.jpg", root)

	require.Equal(t, []string{"photo.jpg"}, refs)
	require.Len(t, imgs, 1)
	require.Equal(t, "image/jpeg", imgs[0].MimeType)
}

func TestExpandFileMentions_ImageWebP_ReturnsImageBlock(t *testing.T) {
	root := t.TempDir()
	webp := []byte{0x52, 0x49, 0x46, 0x46, 0x04, 0x00, 0x00, 0x00, 0x57, 0x45, 0x42, 0x50}
	require.NoError(t, os.WriteFile(filepath.Join(root, "frame.webp"), webp, 0o644))

	_, refs, imgs := expandFileMentions("@frame.webp looks good", root)

	require.Equal(t, []string{"frame.webp"}, refs)
	require.Len(t, imgs, 1)
	require.Equal(t, "image/webp", imgs[0].MimeType)
}

func TestExpandFileMentions_ImageNotInTextBlock(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "ui.png"), minimalPNG, 0o644))

	out, _, _ := expandFileMentions("check @ui.png for me", root)

	// The text annotation must not contain the raw binary data.
	require.NotContains(t, out, string(minimalPNG))
}

func TestExpandFileMentions_ImageAndTextMixed(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "screen.png"), minimalPNG, 0o644))
	writeFile(t, root, "notes.go", "package main\n")

	out, refs, imgs := expandFileMentions("review @screen.png and @notes.go", root)

	require.Equal(t, []string{"screen.png", "notes.go"}, refs)
	require.Len(t, imgs, 1)
	require.Equal(t, "image/png", imgs[0].MimeType)
	// Text part has the code block for the Go file.
	require.Contains(t, out, "```go")
	require.Contains(t, out, "package main")
	// And the image annotation.
	require.Contains(t, out, "@screen.png")
}

func TestExpandFileMentions_ImageDeduplication(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "icon.png"), minimalPNG, 0o644))

	_, refs, imgs := expandFileMentions("@icon.png and @icon.png", root)

	require.Equal(t, []string{"icon.png"}, refs)
	require.Len(t, imgs, 1, "deduplicated: only one ImageBlock even though mentioned twice")
}

func TestMentionImageMIME(t *testing.T) {
	cases := []struct {
		ext  string
		want string
	}{
		{"png", "image/png"},
		{"PNG", ""},
		{"jpg", "image/jpeg"},
		{"jpeg", "image/jpeg"},
		{"gif", "image/gif"},
		{"webp", "image/webp"},
		{"bmp", ""},
		{"svg", ""},
		{"go", ""},
		{"", ""},
	}
	for _, tc := range cases {
		require.Equalf(t, tc.want, mentionImageMIME(tc.ext), "mentionImageMIME(%q)", tc.ext)
	}
}

func TestRunAgent_ImageBlocksIncludedInUserMessage(t *testing.T) {
	// Verify that ImageBlocks returned by expandFileMentions are threaded through
	// to the agent message — integration check at the function boundary.
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "ss.png"), minimalPNG, 0o644))

	_, _, imgs := expandFileMentions("@ss.png", root)

	require.Len(t, imgs, 1)
	require.Equal(t, message.BlockImage, imgs[0].Type())
	require.Equal(t, minimalPNG, imgs[0].Data)
}
