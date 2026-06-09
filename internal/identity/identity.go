package identity

import (
	"regexp"
	"strings"
)

const (
	Name = "BharatCode"

	ShortAnswer = "I am BharatCode, a terminal-based AI coding agent that helps inspect, edit, and verify software projects from your command line. I use your configured model/provider plus local tools and repository context to help with coding tasks."
)

var identityPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bwho\s+are\s+you\b`),
	regexp.MustCompile(`(?i)\bwhat\s+are\s+you\b`),
	regexp.MustCompile(`(?i)\bwhat\s+is\s+bharat\s*code\b`),
	regexp.MustCompile(`(?i)\bwhat\s+is\s+bharatcode\b`),
	regexp.MustCompile(`(?i)\btell\s+me\s+about\s+(yourself|bharat\s*code|bharatcode)\b`),
	regexp.MustCompile(`(?i)\bare\s+you\s+(chatgpt|openai|codex|codex\s+cli|claude|opencode)\b`),
	regexp.MustCompile(`(?i)\bidentify\s+yourself\b`),
}

var repoContextCues = []string{
	"repo", "repository", "codebase", "project", "version", "config",
	"configuration", "provider", "model", "environment", "installed",
}

// Answer returns BharatCode's deterministic identity response for simple
// identity/about prompts. The second return value is false when the prompt
// should be handled by the agent because it asks about repo/config state too.
func Answer(prompt string) (string, bool) {
	text := strings.TrimSpace(prompt)
	if text == "" {
		return "", false
	}
	lower := strings.ToLower(text)
	for _, cue := range repoContextCues {
		if strings.Contains(lower, cue) {
			return "", false
		}
	}
	for _, pattern := range identityPatterns {
		if pattern.MatchString(text) {
			return ShortAnswer, true
		}
	}
	return "", false
}
