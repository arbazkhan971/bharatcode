// Package message defines the canonical conversation representation for BharatCode.
package message

import "unicode/utf8"

// The escaping tables and appendEscapedString below are ported verbatim from
// the Go standard library's encoding/json (encode.go appendString and
// tables.go safeSet/htmlSafeSet) so that hand-rolled string encoding is
// byte-for-byte identical to json.Marshal with HTML escaping enabled. The
// byte-identity tests assert this against json.Marshal directly, so any
// upstream behavior change is caught rather than silently diverging.

// hexDigits maps a nibble to its lowercase hexadecimal digit, matching
// encoding/json's hex constant.
const hexDigits = "0123456789abcdef"

// runeError is U+FFFD, the Unicode replacement character.
const runeError = '�'

// lineSeparator and paragraphSeparator are escaped unconditionally by
// encoding/json because they are valid JSON but unsafe in JSONP.
const (
	lineSeparator      = ' '
	paragraphSeparator = ' '
)

// safeSet holds the value true if the ASCII character with the given index can
// appear inside a JSON string without escaping. All values are true except the
// ASCII control characters (0-31), the double quote, and the backslash.
var safeSet = [utf8.RuneSelf]bool{
	' ': true, '!': true, '"': false, '#': true, '$': true, '%': true,
	'&': true, '\'': true, '(': true, ')': true, '*': true, '+': true,
	',': true, '-': true, '.': true, '/': true, '0': true, '1': true,
	'2': true, '3': true, '4': true, '5': true, '6': true, '7': true,
	'8': true, '9': true, ':': true, ';': true, '<': true, '=': true,
	'>': true, '?': true, '@': true, 'A': true, 'B': true, 'C': true,
	'D': true, 'E': true, 'F': true, 'G': true, 'H': true, 'I': true,
	'J': true, 'K': true, 'L': true, 'M': true, 'N': true, 'O': true,
	'P': true, 'Q': true, 'R': true, 'S': true, 'T': true, 'U': true,
	'V': true, 'W': true, 'X': true, 'Y': true, 'Z': true, '[': true,
	'\\': false, ']': true, '^': true, '_': true, '`': true, 'a': true,
	'b': true, 'c': true, 'd': true, 'e': true, 'f': true, 'g': true,
	'h': true, 'i': true, 'j': true, 'k': true, 'l': true, 'm': true,
	'n': true, 'o': true, 'p': true, 'q': true, 'r': true, 's': true,
	't': true, 'u': true, 'v': true, 'w': true, 'x': true, 'y': true,
	'z': true, '{': true, '|': true, '}': true, '~': true, '': true,
}

// htmlSafeSet holds the value true if the ASCII character with the given index
// can appear inside a JSON string embedded in HTML without escaping. It differs
// from safeSet by also marking '<', '>', and '&' unsafe.
var htmlSafeSet = [utf8.RuneSelf]bool{
	' ': true, '!': true, '"': false, '#': true, '$': true, '%': true,
	'&': false, '\'': true, '(': true, ')': true, '*': true, '+': true,
	',': true, '-': true, '.': true, '/': true, '0': true, '1': true,
	'2': true, '3': true, '4': true, '5': true, '6': true, '7': true,
	'8': true, '9': true, ':': true, ';': true, '<': false, '=': true,
	'>': false, '?': true, '@': true, 'A': true, 'B': true, 'C': true,
	'D': true, 'E': true, 'F': true, 'G': true, 'H': true, 'I': true,
	'J': true, 'K': true, 'L': true, 'M': true, 'N': true, 'O': true,
	'P': true, 'Q': true, 'R': true, 'S': true, 'T': true, 'U': true,
	'V': true, 'W': true, 'X': true, 'Y': true, 'Z': true, '[': true,
	'\\': false, ']': true, '^': true, '_': true, '`': true, 'a': true,
	'b': true, 'c': true, 'd': true, 'e': true, 'f': true, 'g': true,
	'h': true, 'i': true, 'j': true, 'k': true, 'l': true, 'm': true,
	'n': true, 'o': true, 'p': true, 'q': true, 'r': true, 's': true,
	't': true, 'u': true, 'v': true, 'w': true, 'x': true, 'y': true,
	'z': true, '{': true, '|': true, '}': true, '~': true, '': true,
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
