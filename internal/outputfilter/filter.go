// Package outputfilter implements a declarative 8-stage pipeline for
// reducing noise in command output without reformatting it. Each filter
// targets a specific command (matched by regex), strips predictable noise
// lines, and enforces a line cap — keeping the output looking like real
// command output while cutting token cost by 50%+ on verbose build tools.
//
// Pipeline stages (applied in order):
//  1. strip_ansi           — remove ANSI escape codes
//  2. replace              — regex substitutions, line-by-line, chainable
//  3. match_output         — short-circuit: blob matches → return fixed message
//  4. strip/keep_lines     — drop or keep lines by regex
//  5. truncate_lines_at    — truncate each line to N chars
//  6. tail_lines           — keep only the last N lines
//  7. max_lines            — absolute line cap (first N lines)
//  8. on_empty             — fallback message if result is empty
//
// Filter lookup: builtin filters only (first match wins). The design mirrors
// rtk's declarative filter engine but is implemented natively in Go.
package outputfilter

import (
	"regexp"
	"strings"
)

// ReplaceRule is a single regex substitution applied line-by-line.
type ReplaceRule struct {
	Pattern     *regexp.Regexp
	Replacement string
}

// MatchOutputRule short-circuits the pipeline: if Pattern matches anywhere
// in the full output blob (after strip_ansi + replace), the filter returns
// Message immediately without further processing.
type MatchOutputRule struct {
	Pattern *regexp.Regexp
	Message string
}

// Filter is a compiled, immutable filter definition. All regexes are
// pre-compiled at init time; no allocation occurs on the hot path.
type Filter struct {
	// Name is the canonical filter identifier, matching the command keyword.
	Name string
	// Description is a human-readable summary shown in debug output.
	Description string
	// MatchCommand is matched against the full command string.
	MatchCommand *regexp.Regexp

	// StripANSI strips ANSI escape codes before any other processing.
	StripANSI bool
	// Replace applies regex substitutions line-by-line (stage 2).
	Replace []ReplaceRule
	// MatchOutput short-circuits the pipeline (stage 3).
	MatchOutput []MatchOutputRule
	// StripLinesMatching drops lines matching any of these regexes (stage 4).
	StripLinesMatching []*regexp.Regexp
	// KeepLinesMatching keeps only lines matching at least one regex (stage 4).
	// Mutually exclusive with StripLinesMatching; StripLinesMatching wins if both set.
	KeepLinesMatching []*regexp.Regexp
	// TruncateLinesAt truncates each line to this many bytes (stage 5). 0 = no cap.
	TruncateLinesAt int
	// TailLines keeps only the last N lines (stage 6). 0 = keep all.
	TailLines int
	// MaxLines caps the total output to N lines (stage 7). 0 = no cap.
	MaxLines int
	// OnEmpty is emitted when the filtered output is empty (stage 8).
	OnEmpty string
}

// Apply runs the 8-stage pipeline on output and returns the filtered result.
// If the filter does not match cmd, Apply returns ("", false).
// On a successful filter application the second return is true.
func (f *Filter) Apply(cmd, output string) (string, bool) {
	if !f.MatchCommand.MatchString(cmd) {
		return "", false
	}

	// Stage 1: strip ANSI escape codes.
	if f.StripANSI {
		output = stripANSI(output)
	}

	// Stage 2: line-by-line regex substitutions.
	if len(f.Replace) > 0 {
		lines := strings.Split(output, "\n")
		for i, line := range lines {
			for _, r := range f.Replace {
				line = r.Pattern.ReplaceAllString(line, r.Replacement)
			}
			lines[i] = line
		}
		output = strings.Join(lines, "\n")
	}

	// Stage 3: short-circuit on blob match.
	for _, mo := range f.MatchOutput {
		if mo.Pattern.MatchString(output) {
			return mo.Message, true
		}
	}

	// Stage 4: strip or keep lines.
	if len(f.StripLinesMatching) > 0 {
		output = filterLines(output, f.StripLinesMatching, true)
	} else if len(f.KeepLinesMatching) > 0 {
		output = filterLines(output, f.KeepLinesMatching, false)
	}

	// Stage 5: truncate long lines.
	if f.TruncateLinesAt > 0 {
		output = truncateLines(output, f.TruncateLinesAt)
	}

	// Stage 6: tail_lines — keep last N lines.
	if f.TailLines > 0 {
		output = tailLines(output, f.TailLines)
	}

	// Stage 7: max_lines — keep first N lines.
	if f.MaxLines > 0 {
		output = capLines(output, f.MaxLines)
	}

	// Stage 8: on_empty fallback.
	if strings.TrimSpace(output) == "" {
		if f.OnEmpty != "" {
			return f.OnEmpty, true
		}
		return "", true
	}

	return strings.TrimRight(output, "\n"), true
}

// ansiEscape matches ANSI/VT100 escape sequences.
var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*[mGKHF]|\x1b\[[?][0-9;]*[hlr]|\x1b\[[\d;]*[ABCDJKST]|\x1b=|\x1b>|\x1b\(B|\r`)

func stripANSI(s string) string {
	return ansiEscape.ReplaceAllString(s, "")
}

// filterLines applies strip (strip=true) or keep (strip=false) semantics.
func filterLines(output string, patterns []*regexp.Regexp, strip bool) string {
	lines := strings.Split(output, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		matched := false
		for _, p := range patterns {
			if p.MatchString(line) {
				matched = true
				break
			}
		}
		keep := matched != strip // XOR: strip=true means keep when NOT matched
		if keep {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

// truncateLines truncates each line to at most n bytes.
func truncateLines(output string, n int) string {
	lines := strings.Split(output, "\n")
	for i, line := range lines {
		if len(line) > n {
			lines[i] = line[:n]
		}
	}
	return strings.Join(lines, "\n")
}

// tailLines keeps the last n non-trailing-empty lines.
func tailLines(output string, n int) string {
	// Trim trailing newline before splitting so a terminal "\n" does not
	// count as an extra empty line that would displace real content.
	trimmed := strings.TrimRight(output, "\n")
	lines := strings.Split(trimmed, "\n")
	if len(lines) <= n {
		return output
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}

// capLines keeps the first n lines; appends a truncation notice if needed.
func capLines(output string, n int) string {
	lines := strings.Split(output, "\n")
	if len(lines) <= n {
		return output
	}
	dropped := len(lines) - n
	result := strings.Join(lines[:n], "\n")
	result += "\n[" + itoa(dropped) + " more lines — output capped by outputfilter]"
	return result
}

// itoa converts an int to its decimal string representation without importing strconv.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := [20]byte{}
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}
