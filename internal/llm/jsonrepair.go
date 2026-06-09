package llm

import (
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// RepairToolCallJSON sanitizes a tool-call argument payload that a model
// streamed before it is handed to json.Unmarshal. Frontier models occasionally
// emit JSON that is technically invalid but obviously intended to be a specific
// object: a literal newline or tab inside a string, a stray backslash, a lone
// UTF-16 surrogate that survived a tokenizer boundary, or a body that simply got
// cut off when the response hit a length limit mid-write. The standard decoder
// rejects all of these outright, which would otherwise turn a recoverable tool
// call into a hard failure.
//
// The function walks the input once, rewriting only the characters that a strict
// JSON parser would reject, then makes a best-effort attempt to close any
// brackets, braces, or string that truncation left open. It returns the repaired
// text and a flag reporting whether any change was actually made, so callers can
// keep the original bytes (and the original error, if any) on the common path
// where nothing needed fixing.
//
// The repair is deliberately conservative: it never reorders, drops, or
// reinterprets well-formed content, and a value that is already valid JSON is
// returned byte-for-byte with repaired=false. It is a syntactic safety net, not
// a schema validator — a structurally repaired payload can still fail to satisfy
// the tool's expected shape, which is the caller's concern.
func RepairToolCallJSON(raw string) (string, bool) {
	if raw == "" {
		return raw, false
	}

	var b strings.Builder
	b.Grow(len(raw) + 8)

	repaired := false
	// inString tracks whether the cursor sits inside a JSON string literal, where
	// escaping rules differ from structural text. depthStack records the unclosed
	// structural openers ('{' / '[') in order so truncated input can be closed
	// with the matching characters in the correct sequence.
	inString := false
	var depthStack []byte

	for i := 0; i < len(raw); {
		c := raw[i]

		if !inString {
			switch c {
			case '"':
				inString = true
				b.WriteByte(c)
				i++
			case '{':
				depthStack = append(depthStack, '}')
				b.WriteByte(c)
				i++
			case '[':
				depthStack = append(depthStack, ']')
				b.WriteByte(c)
				i++
			case '}', ']':
				// Pop the matching opener when the closer is the one we expect. A
				// mismatched closer is left as-is; the structural-close pass below
				// will not be reached for genuinely malformed nesting, and the
				// decoder remains the source of truth for that.
				if n := len(depthStack); n > 0 && depthStack[n-1] == c {
					depthStack = depthStack[:n-1]
				}
				b.WriteByte(c)
				i++
			default:
				b.WriteByte(c)
				i++
			}
			continue
		}

		// Inside a string literal.
		switch {
		case c == '"':
			inString = false
			b.WriteByte(c)
			i++
		case c == '\\':
			// An escape sequence: validate the character that follows and rewrite
			// it when the model produced something JSON does not permit.
			if i+1 >= len(raw) {
				// A trailing backslash at end-of-input cannot begin a valid escape
				// (truncation cut it off); escape it so the lone byte survives, and
				// let the close pass terminate the string.
				b.WriteString(`\\`)
				repaired = true
				i++
				continue
			}
			next := raw[i+1]
			switch next {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				b.WriteByte(c)
				b.WriteByte(next)
				i += 2
			case 'u':
				// A \uXXXX escape. Validate the four hex digits and, when this is a
				// high surrogate, the optional trailing low surrogate, dropping any
				// half of a pair that is unpaired or malformed.
				consumed, out, changed := repairUnicodeEscape(raw[i:])
				b.WriteString(out)
				if changed {
					repaired = true
				}
				i += consumed
			default:
				// An invalid escape such as \x or \z: a strict parser errors here,
				// but the model almost always meant a literal backslash followed by
				// the character, so escape the backslash and keep the next byte.
				b.WriteString(`\\`)
				repaired = true
				i++
			}
		case c <= 0x1f:
			// A raw control character (newline, tab, NUL, ...) is illegal inside a
			// JSON string and must be escaped. Use the short forms JSON defines for
			// the common whitespace controls and the \u00XX form for the rest.
			b.WriteString(escapeControl(c))
			repaired = true
			i++
		default:
			// A normal string byte. Copy a full UTF-8 rune at a time so a multi-byte
			// sequence is never split, and replace an invalid encoding with the
			// Unicode replacement character rather than emit broken UTF-8.
			r, size := utf8.DecodeRuneInString(raw[i:])
			if r == utf8.RuneError && size == 1 {
				b.WriteRune(utf8.RuneError)
				repaired = true
				i++
				continue
			}
			b.WriteString(raw[i : i+size])
			i += size
		}
	}

	// Best-effort truncation recovery. A string left open by a cut-off response is
	// closed first, then every unclosed structural container is closed in
	// last-opened-first order so the brackets nest correctly.
	if inString {
		b.WriteByte('"')
		repaired = true
	}
	for n := len(depthStack) - 1; n >= 0; n-- {
		b.WriteByte(depthStack[n])
		repaired = true
	}

	if !repaired {
		// Nothing was rewritten; hand back the original bytes untouched so callers
		// on the happy path pay nothing and observe identical content.
		return raw, false
	}
	return b.String(), true
}

// escapeControl renders a control character (0x00-0x1f) as a JSON-legal escape,
// preferring the two-character short forms the spec defines for the common
// whitespace controls and falling back to the six-character \u00XX form for the
// remainder.
func escapeControl(c byte) string {
	switch c {
	case '\b':
		return `\b`
	case '\f':
		return `\f`
	case '\n':
		return `\n`
	case '\r':
		return `\r`
	case '\t':
		return `\t`
	default:
		const hex = "0123456789abcdef"
		return `\u00` + string([]byte{hex[c>>4], hex[c&0xf]})
	}
}

// repairUnicodeEscape inspects a \uXXXX escape at the start of s (s[0:2] is known
// to be "\\u") and returns the number of input bytes consumed, the text to emit
// in their place, and whether the emitted text differs from the input.
//
// It enforces the well-formedness rules a strict decoder applies to \u escapes:
// the four hex digits must be present and valid, and a high surrogate
// (\uD800-\uDBFF) is only legal when immediately followed by a low surrogate
// (\uDC00-\uDFFF). A lone or malformed surrogate is replaced with the Unicode
// replacement character (�) so the surrounding string stays decodable;
// truncated digits are likewise collapsed to the replacement character.
func repairUnicodeEscape(s string) (consumed int, out string, changed bool) {
	hi, ok := parseHex4(s)
	if !ok {
		// Fewer than four valid hex digits follow \u — truncation or a typo. Emit
		// the replacement character and consume whatever short, broken run exists
		// (the backslash, the 'u', and any hex digits actually present) so the
		// cursor advances past the damaged escape.
		return brokenEscapeLen(s), string(utf8.RuneError), true
	}

	// A standalone value that is not part of a surrogate pair (including a lone low
	// surrogate, which can never lead a pair) passes through unchanged.
	if !utf16.IsSurrogate(rune(hi)) {
		return 6, s[:6], false
	}
	if hi < 0xDC00 {
		// High surrogate: a valid pair requires an immediately following low
		// surrogate escape. Verify it and keep both escapes verbatim if present.
		if len(s) >= 12 && s[6] == '\\' && s[7] == 'u' {
			if lo, ok := parseHex4(s[6:]); ok && lo >= 0xDC00 && lo <= 0xDFFF {
				return 12, s[:12], false
			}
		}
		// Unpaired high surrogate: drop it for the replacement character.
		return 6, string(utf8.RuneError), true
	}
	// Lone low surrogate (0xDC00-0xDFFF with no preceding high surrogate): also
	// invalid on its own.
	return 6, string(utf8.RuneError), true
}

// parseHex4 reports the value of the four hex digits following a "\u" prefix in
// s, requiring s to begin with the two escape-introducer bytes and hold at least
// four hex digits after them. It returns false when any digit is missing or not a
// valid hexadecimal character.
func parseHex4(s string) (uint16, bool) {
	if len(s) < 6 {
		return 0, false
	}
	var v uint16
	for k := 2; k < 6; k++ {
		d, ok := hexVal(s[k])
		if !ok {
			return 0, false
		}
		v = v<<4 | uint16(d)
	}
	return v, true
}

// hexVal maps a single ASCII hexadecimal digit to its numeric value.
func hexVal(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	default:
		return 0, false
	}
}

// brokenEscapeLen returns how many bytes a malformed \u escape at the start of s
// occupies so the scanner can step past it. It counts the leading "\\u" plus the
// run of hex digits that actually follow (fewer than the required four), bounded
// by the input length.
func brokenEscapeLen(s string) int {
	// s begins with "\\u"; advance over any partial hex run that follows.
	n := 2
	for n < len(s) && n < 6 {
		if _, ok := hexVal(s[n]); !ok {
			break
		}
		n++
	}
	return n
}
