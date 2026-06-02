// Package message defines the canonical conversation representation for BharatCode.
package message

import "regexp"

// RedactPlaceholder is substituted in place of any value matched as a likely
// secret by RedactText.
const RedactPlaceholder = "[REDACTED]"

// redactPatterns holds the compiled regexes applied, in order, by RedactText.
// Patterns are intentionally scoped to secret-indicating shapes so ordinary
// prose and code (for example "MAX_RETRIES=5" or "count = 5") survive intact.
var redactPatterns = []*regexp.Regexp{
	// PEM private-key blocks: redact the whole block, not just the header, so
	// the key body never survives in cleartext.
	regexp.MustCompile(`(?s)-----BEGIN[^-]*PRIVATE KEY-----.*?-----END[^-]*PRIVATE KEY-----`),
	// OpenAI-style keys: "sk-" followed by a long token. The length and
	// charset floor keep it off ordinary words that merely start with "sk-".
	regexp.MustCompile(`sk-[A-Za-z0-9_-]{20,}`),
	// AWS access key IDs: a fixed "AKIA" prefix and 16 upper-alphanumerics.
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	// GitHub personal-access / app tokens (ghp_, gho_, ghu_, ghs_, ghr_).
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{20,}`),
	// Bearer / authorization tokens, for example "Bearer xyz".
	regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._\-/+=]+`),
	// Secret-bearing KEY=value / KEY: value assignments. The key name is
	// scoped to secret-indicating tokens (optionally with a prefix such as
	// "AWS_" or "MY-") so benign assignments like "MAX_RETRIES=5" are
	// preserved while "AWS_SECRET=..." is caught.
	regexp.MustCompile(`(?i)\b[\w-]*(?:api[_-]?key|secret|access[_-]?key|auth[_-]?token|password|passwd|token)\s*[:=]\s*\S+`),
}

// RedactText scrubs likely secrets from s and returns the result. It targets
// API keys, AWS access keys, GitHub tokens, bearer tokens, PEM private-key
// blocks, and common secret-bearing KEY=value assignments, replacing each
// match with RedactPlaceholder. Ordinary text and code are left untouched.
//
// RedactText errs toward preserving content: it only rewrites text matching a
// well-known secret shape, so it is best-effort rather than exhaustive.
func RedactText(s string) string {
	for _, re := range redactPatterns {
		s = re.ReplaceAllString(s, RedactPlaceholder)
	}
	return s
}

// Redacted returns a copy of m with likely secrets scrubbed from its textual
// content, suitable for safe logging or export. TextBlock, ToolResultBlock,
// and ThinkingBlock bodies are passed through RedactText; ToolUseBlock.Input
// is left untouched because rewriting its raw JSON could corrupt it, and
// ImageBlock data is not text. Redacted never mutates the receiver.
func (m Message) Redacted() Message {
	out := m
	out.Usage = m.Usage
	if m.Content == nil {
		out.Content = nil
		return out
	}

	content := make([]ContentBlock, len(m.Content))
	for i, block := range m.Content {
		switch b := block.(type) {
		case TextBlock:
			b.Text = RedactText(b.Text)
			content[i] = b
		case ToolResultBlock:
			b.Content = RedactText(b.Content)
			content[i] = b
		case ThinkingBlock:
			b.Text = RedactText(b.Text)
			content[i] = b
		default:
			content[i] = block
		}
	}
	out.Content = content
	return out
}
