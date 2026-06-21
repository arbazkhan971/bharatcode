package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/util"
)

// mentionPattern matches an @-file reference: an "@" at the start of the input
// or following whitespace or an opening bracket, then a path made of common
// filename characters. Requiring "@" to begin the token (rather than follow a
// path character) excludes email addresses (user@host) and other mid-token
// "@" uses, which are never treated as file references.
var mentionPattern = regexp.MustCompile(`(^|[\s(\[{])@([A-Za-z0-9._/\-]+)`)

const (
	// maxMentionFileBytes caps how much of a single referenced file is inlined.
	// Larger files are truncated with a notice so one @mention cannot dominate
	// the context window.
	maxMentionFileBytes = 100 * 1024
	// maxMentionTotalBytes caps the combined size of all inlined files in a
	// single prompt, so a message full of @mentions stays bounded.
	maxMentionTotalBytes = 256 * 1024
	// maxMentionImageBytes caps each image attached via an @-mention so a single
	// large screenshot cannot exhaust the context window or exceed vision-API limits.
	maxMentionImageBytes = 4 * 1024 * 1024
)

// mentionTrailingPunct is stripped from the end of a candidate path so prose
// like "see @main.go." or "(@pkg/x.go)" still resolves to the real file.
const mentionTrailingPunct = ".,:;!?)]}"

// expandFileMentions scans text for @-file references that resolve to readable
// regular files inside root and appends their contents to the prompt as an
// "[Attached files]" section. The original text is preserved verbatim so the
// user's @mention stays visible; the model additionally receives the file
// bodies as context, matching common @-file conventions in coding agents.
//
// Image files (PNG/JPEG/GIF/WebP) are returned as ImageBlocks rather than
// fenced text so vision-capable models can inspect them directly. A compact
// annotation like "@shot.png [image/png, 45KB]" is added in the text section
// to keep the reference visible in the conversation.
//
// It returns the expanded prompt, the workspace-relative paths that were
// resolved (first-mention order, de-duplicated), and any image blocks. When
// nothing resolves, the original text, a nil slice, and a nil slice are returned.
func expandFileMentions(text, root string) (string, []string, []message.ImageBlock) {
	if root == "" || !strings.Contains(text, "@") {
		return text, nil, nil
	}

	matches := mentionPattern.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return text, nil, nil
	}

	var (
		refs   []string
		blocks []string
		images []message.ImageBlock
		seen   = make(map[string]bool)
		total  int
	)
	for _, m := range matches {
		rel, abs, ok := resolveMention(m[2], root)
		if !ok || seen[rel] {
			continue
		}
		seen[rel] = true

		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(rel), "."))
		if mime := mentionImageMIME(ext); mime != "" {
			data, err := os.ReadFile(abs)
			if err != nil || len(data) == 0 {
				continue
			}
			refs = append(refs, rel)
			if len(data) > maxMentionImageBytes {
				blocks = append(blocks, fmt.Sprintf("@%s [image too large to attach: %s]\n", rel, util.HumanBytes(int64(len(data)))))
			} else {
				images = append(images, message.ImageBlock{MimeType: mime, Data: data})
				blocks = append(blocks, fmt.Sprintf("@%s [attached image: %s, %s]\n", rel, mime, util.HumanBytes(int64(len(data)))))
			}
			continue
		}

		data, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		if total >= maxMentionTotalBytes {
			continue
		}
		body, truncated := clampMention(data, maxMentionTotalBytes-total)
		total += len(body)

		refs = append(refs, rel)
		blocks = append(blocks, renderMentionBlock(rel, body, truncated))
	}

	if len(blocks) == 0 {
		return text, nil, nil
	}

	var b strings.Builder
	b.WriteString(text)
	b.WriteString("\n\n[Attached files]\n")
	for _, blk := range blocks {
		b.WriteString(blk)
	}
	return b.String(), refs, images
}

// mentionImageMIME returns the MIME type for recognised image file extensions,
// or "" for non-image files. Only formats with broad vision-API support are
// treated as images; unrecognised extensions fall back to text inlining.
func mentionImageMIME(ext string) string {
	switch ext {
	case "png":
		return "image/png"
	case "jpg", "jpeg":
		return "image/jpeg"
	case "gif":
		return "image/gif"
	case "webp":
		return "image/webp"
	default:
		return ""
	}
}

// resolveMention turns a raw @mention token into a clean workspace-relative
// path and its absolute path, reporting ok only when it names an existing
// regular file inside root. Trailing prose punctuation is stripped so a
// sentence-final mention still resolves.
func resolveMention(token, root string) (rel, abs string, ok bool) {
	token = strings.TrimRight(token, mentionTrailingPunct)
	if token == "" {
		return "", "", false
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", "", false
	}

	candidate := filepath.FromSlash(token)
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(absRoot, candidate)
	}
	candidate = filepath.Clean(candidate)

	rel, err = filepath.Rel(absRoot, candidate)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		// The path escapes the workspace; never inline files outside root.
		return "", "", false
	}

	info, err := os.Stat(candidate)
	if err != nil || !info.Mode().IsRegular() {
		return "", "", false
	}
	return filepath.ToSlash(rel), candidate, true
}

// clampMention truncates data to at most limit bytes (and never more than
// maxMentionFileBytes), reporting whether truncation occurred. Truncation
// happens on a UTF-8 boundary-agnostic byte cut, which is fine for fenced
// display.
func clampMention(data []byte, limit int) ([]byte, bool) {
	max := maxMentionFileBytes
	if limit < max {
		max = limit
	}
	if max < 0 {
		max = 0
	}
	if len(data) <= max {
		return data, false
	}
	return data[:max], true
}

// renderMentionBlock formats one inlined file as a fenced block tagged with the
// path and a language hint derived from its extension.
func renderMentionBlock(rel string, body []byte, truncated bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "@%s:\n```%s\n", rel, mentionLang(rel))
	b.Write(body)
	if len(body) > 0 && body[len(body)-1] != '\n' {
		b.WriteByte('\n')
	}
	if truncated {
		b.WriteString("… [truncated]\n")
	}
	b.WriteString("```\n")
	return b.String()
}

// mentionLang maps a file to a fenced-code language hint. It first checks the
// base name for well-known files that carry no informative extension — a
// Dockerfile, a Makefile, go.mod — common well-known files identified by name
// rather than suffix; it then falls back to the extension.
// An unrecognized name with no known extension yields an empty hint, which
// renders as a plain block.
func mentionLang(rel string) string {
	if lang := mentionLangByName(strings.ToLower(filepath.Base(rel))); lang != "" {
		return lang
	}
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(rel), "."))
	switch ext {
	case "go":
		return "go"
	case "py":
		return "python"
	case "js", "mjs", "cjs":
		return "javascript"
	case "ts":
		return "typescript"
	case "tsx", "jsx":
		return ext
	case "rs":
		return "rust"
	case "java":
		return "java"
	case "rb":
		return "ruby"
	case "kt", "kts":
		return "kotlin"
	case "swift":
		return "swift"
	case "php":
		return "php"
	case "scala":
		return "scala"
	case "sh", "bash", "zsh":
		return "bash"
	case "json":
		return "json"
	case "yaml", "yml":
		return "yaml"
	case "toml":
		return "toml"
	case "md", "markdown":
		return "markdown"
	case "html":
		return "html"
	case "css":
		return "css"
	case "scss", "sass":
		return "scss"
	case "sql":
		return "sql"
	case "c", "h":
		return "c"
	case "cpp", "cc", "hpp", "cxx":
		return "cpp"
	case "cs":
		return "csharp"
	case "lua":
		return "lua"
	case "dart":
		return "dart"
	case "ex", "exs":
		return "elixir"
	case "erl", "hrl":
		return "erlang"
	case "clj", "cljs", "cljc", "edn":
		return "clojure"
	case "hs":
		return "haskell"
	case "pl", "pm":
		return "perl"
	case "r":
		return "r"
	case "vue":
		return "vue"
	case "svelte":
		return "svelte"
	case "proto":
		return "protobuf"
	case "graphql", "gql":
		return "graphql"
	case "tf", "tfvars", "hcl":
		return "hcl"
	case "groovy", "gradle":
		return "groovy"
	case "ps1", "psm1":
		return "powershell"
	case "zig":
		return "zig"
	case "diff", "patch":
		return "diff"
	case "xml":
		return "xml"
	case "ini", "cfg", "conf":
		return "ini"
	case "dockerfile":
		return "dockerfile"
	case "mk":
		return "makefile"
	default:
		return ""
	}
}

// mentionLangByName maps a lower-cased base name to a fenced-code language hint
// for well-known files that carry no informative extension (Dockerfile,
// Makefile) or whose tag conventionally follows the whole name (go.mod). It
// returns "" when the name is not special, leaving extension-based detection to
// the caller.
func mentionLangByName(base string) string {
	switch base {
	case "dockerfile", "containerfile":
		return "dockerfile"
	case "makefile", "gnumakefile":
		return "makefile"
	case "go.mod", "go.sum":
		return "go"
	case "cmakelists.txt":
		return "cmake"
	case ".gitignore", ".dockerignore", ".gitattributes":
		return "gitignore"
	case ".env":
		return "bash"
	default:
		return ""
	}
}
