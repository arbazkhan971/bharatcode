// Package message defines the canonical conversation representation for BharatCode.
package message

import "unicode/utf8"

// The escaping logic below is ported verbatim from the Go standard library's
// encoding/json (encode.go appendString and tables.go htmlSafeSet) so that
// hand-rolled string encoding is byte-for-byte identical to json.Marshal with
// HTML escaping enabled. The byte-identity tests assert this against
// json.Marshal directly, so any upstream behavior change is caught rather than
// silently diverging.

// hexDigits maps a nibble to its lowercase hexadecimal digit, matching
// encoding/json's hex constant.
const hexDigits = "0123456789abcdef"

// runeError is U+FFFD, the Unicode replacement character.
const runeError = '�'

// lineSeparator (U+2028) and paragraphSeparator (U+2029) are escaped
// unconditionally by encoding/json because they are valid JSON but unsafe in
// JSONP.
const (
	lineSeparator      = ' '
	paragraphSeparator = ' '
)

// htmlSafeSet holds the value true if the ASCII character with the given index
// can appear inside a JSON string embedded in HTML without escaping. All values
// are true except the ASCII control characters (0x00-0x1F), the double quote,
// the backslash, and the HTML-sensitive '<', '>', and '&'. DEL (0x7F) is safe.
// Marshaling always escapes HTML, so this is the only table required.
var htmlSafeSet = buildHTMLSafeSet()

// buildHTMLSafeSet constructs the htmlSafeSet table programmatically so the
// unsafe bytes are spelled out explicitly rather than relying on fragile
// literal control or DEL glyphs in a composite literal.
func buildHTMLSafeSet() [utf8.RuneSelf]bool {
	var set [utf8.RuneSelf]bool
	for b := 0x20; b < utf8.RuneSelf; b++ {
		set[b] = true
	}
	set['"'] = false
	set['\\'] = false
	set['<'] = false
	set['>'] = false
	set['&'] = false
	return set
}

// appendEscapedString appends src to dst as a quoted, HTML-escaped JSON string,
// reproducing encoding/json's appendString with escapeHTML=true. It is the
// single source of string escaping for the optimized Message marshaler.
func appendEscapedString(dst []byte, src string) []byte {
	dst = append(dst, '"')
	start := 0
	for i := 0; i < len(src); {
		if b := src[i]; b < utf8.RuneSelf {
			if htmlSafeSet[b] {
				i++
				continue
			}
			dst = append(dst, src[start:i]...)
			switch b {
			case '\\', '"':
				dst = append(dst, '\\', b)
			case '\b':
				dst = append(dst, '\\', 'b')
			case '\f':
				dst = append(dst, '\\', 'f')
			case '\n':
				dst = append(dst, '\\', 'n')
			case '\r':
				dst = append(dst, '\\', 'r')
			case '\t':
				dst = append(dst, '\\', 't')
			default:
				// Bytes < 0x20 other than \b, \f, \n, \r, \t, plus the
				// HTML-sensitive <, >, & are escaped as \u00XX.
				dst = append(dst, '\\', 'u', '0', '0', hexDigits[b>>4], hexDigits[b&0xF])
			}
			i++
			start = i
			continue
		}
		n := min(len(src)-i, utf8.UTFMax)
		c, size := utf8.DecodeRuneInString(src[i : i+n])
		if c == runeError && size == 1 {
			dst = append(dst, src[start:i]...)
			dst = append(dst, '\\', 'u', 'f', 'f', 'f', 'd')
			i += size
			start = i
			continue
		}
		if c == lineSeparator || c == paragraphSeparator {
			dst = append(dst, src[start:i]...)
			dst = append(dst, '\\', 'u', '2', '0', '2', hexDigits[c&0xF])
			i += size
			start = i
			continue
		}
		i += size
	}
	dst = append(dst, src[start:]...)
	dst = append(dst, '"')
	return dst
}
