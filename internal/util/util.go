// Package util provides small stateless helpers used across BharatCode.
// It depends only on the Go standard library.
package util

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// isWindows is a package-level variable to allow overriding in tests.
var isWindows = runtime.GOOS == "windows"

// ExpandPath expands a leading `~` to the user's home directory and
// substitutes environment variables of the form `$VAR` and `${VAR}`.
// The result is cleaned with filepath.Clean. An empty input returns
// an empty string. ExpandPath never returns an error; unknown
// variables expand to the empty string, matching os.ExpandEnv.
func ExpandPath(p string) string {
	if p == "" {
		return ""
	}
	// First substitute environment variables.
	p = os.ExpandEnv(p)
	// Check if it starts with "~".
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "~\\") {
		home, err := os.UserHomeDir()
		if err == nil {
			if p == "~" {
				p = home
			} else {
				p = filepath.Join(home, p[2:])
			}
		}
	}
	return filepath.Clean(p)
}

// ShortPath replaces a leading home-directory prefix with `~`. It is
// the inverse of ExpandPath for display purposes. Returns p unchanged
// if it is not under the home directory or if the home directory
// cannot be determined.
func ShortPath(p string) string {
	if p == "" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	home = filepath.Clean(home)
	cleanedP := filepath.Clean(p)

	equal := false
	if isWindows {
		equal = strings.EqualFold(cleanedP, home)
	} else {
		equal = (cleanedP == home)
	}
	if equal {
		return "~"
	}

	prefix := home + string(filepath.Separator)
	hasPrefix := false
	if isWindows {
		hasPrefix = len(cleanedP) > len(prefix) && strings.EqualFold(cleanedP[:len(prefix)], prefix)
	} else {
		hasPrefix = strings.HasPrefix(cleanedP, prefix)
	}

	if hasPrefix {
		return "~" + string(filepath.Separator) + cleanedP[len(prefix):]
	}
	return p
}

// HumanBytes formats a byte count using SI-style binary units up to
// PiB (1024-based). Examples: 0 → "0 B", 1536 → "1.5 KB",
// 5_242_880 → "5.0 MB". Negative inputs are formatted with a leading
// minus sign.
func HumanBytes(n int64) string {
	isNeg := n < 0
	var u uint64
	if isNeg {
		if n == -9223372036854775808 {
			u = 9223372036854775808
		} else {
			u = uint64(-n)
		}
	} else {
		u = uint64(n)
	}

	var buf [32]byte
	out := buf[:0]

	if isNeg {
		out = append(out, '-')
	}

	if u < 1024 {
		out = strconv.AppendUint(out, u, 10)
		out = append(out, ' ', 'B')
		return string(out)
	}

	suffixes := []string{"KB", "MB", "GB", "TB", "PB"}
	divisors := []uint64{
		1024,
		1024 * 1024,
		1024 * 1024 * 1024,
		1024 * 1024 * 1024 * 1024,
		1024 * 1024 * 1024 * 1024 * 1024,
	}

	idx := 0
	for i := 0; i < len(divisors); i++ {
		if i == len(divisors)-1 || u/divisors[i+1] == 0 {
			idx = i
			break
		}
	}

	div := divisors[idx]
	suffix := suffixes[idx]

	f := float64(u) / float64(div)
	out = strconv.AppendFloat(out, f, 'f', 1, 64)
	out = append(out, ' ')
	out = append(out, suffix...)
	return string(out)
}

// HumanDuration formats a non-negative duration as a compact
// human-readable string. Examples: 750ms → "750ms", 12s → "12s",
// 154s → "2m 34s", 3725s → "1h 2m 5s". Durations below one
// millisecond are reported in microseconds.
func HumanDuration(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	if d < time.Millisecond {
		us := d / time.Microsecond
		var buf [32]byte
		out := buf[:0]
		out = strconv.AppendInt(out, int64(us), 10)
		out = append(out, "µs"...)
		return string(out)
	}

	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second
	d -= s * time.Second
	ms := d / time.Millisecond

	var buf [64]byte
	out := buf[:0]

	first := true
	if h > 0 {
		out = strconv.AppendInt(out, int64(h), 10)
		out = append(out, 'h')
		first = false
	}
	if m > 0 {
		if !first {
			out = append(out, ' ')
		}
		out = strconv.AppendInt(out, int64(m), 10)
		out = append(out, 'm')
		first = false
	}
	if s > 0 {
		if !first {
			out = append(out, ' ')
		}
		out = strconv.AppendInt(out, int64(s), 10)
		out = append(out, 's')
		first = false
	}
	if ms > 0 {
		if !first {
			out = append(out, ' ')
		}
		out = strconv.AppendInt(out, int64(ms), 10)
		out = append(out, 'm', 's')
	}

	return string(out)
}

// Truncate returns s clipped to max runes. If s is longer than max
// the result ends with the single-character ellipsis "…" (which
// counts toward max). A max of zero or less returns the empty
// string. Truncate is rune-safe and never splits a multi-byte
// codepoint.
func Truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runeCount := utf8.RuneCountInString(s)
	if runeCount <= max {
		return s
	}

	var byteIdx int
	for i := 0; i < max-1; i++ {
		_, size := utf8.DecodeRuneInString(s[byteIdx:])
		byteIdx += size
	}
	return s[:byteIdx] + "…"
}

// Indent prefixes every line of s with prefix, including the final
// line when it is non-empty. Line endings are preserved exactly as
// found ("\n", "\r\n", or none).
func Indent(s string, prefix string) string {
	if s == "" {
		return ""
	}
	n := 0
	if len(s) > 0 {
		n = 1
	}
	for i := 0; i < len(s)-1; i++ {
		if s[i] == '\n' {
			n++
		}
	}

	var sb strings.Builder
	sb.Grow(len(s) + n*len(prefix))

	lineStart := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			sb.WriteString(prefix)
			sb.WriteString(s[lineStart : i+1])
			lineStart = i + 1
		}
	}
	if lineStart < len(s) {
		sb.WriteString(prefix)
		sb.WriteString(s[lineStart:])
	}
	return sb.String()
}
