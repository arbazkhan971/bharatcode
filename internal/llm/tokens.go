package llm

import (
	"context"
	"unicode/utf8"

	"github.com/arbazkhan971/bharatcode/internal/message"
)

// Token-estimation weights, expressed in tokens contributed per source rune.
//
// The naive heuristic the agent uses today is bytes/4: it assumes ~4 bytes per
// token, which is a good fit for plain ASCII English (most BPE vocabularies pack
// roughly four English characters into a token) but badly undercounts scripts
// whose characters are several UTF-8 bytes yet cost on the order of one whole
// token each. The classic example is CJK text: a Han character is ~3 UTF-8 bytes
// (bytes/4 => ~0.75 tokens) but cl100k/o200k-style tokenizers spend roughly
// 1-2 tokens on it, so bytes/4 structurally underestimates by 30-60%.
//
// EstimateTokens walks runes and weights each by category so the estimate
// tracks real tokenizers more closely while staying dependency-free and
// deterministic. The ASCII alphanumeric weight is deliberately 0.25 so that on
// a pure-ASCII-letters string the result reproduces the bytes/4 baseline
// exactly (1 byte/char * 0.25 == len/4); we never regress on English. Every
// category contributes a strictly positive amount, which keeps the estimate
// monotonic: appending any text can only raise the count.
const (
	// weightASCIIAlnum matches the bytes/4 baseline on ASCII letters and digits
	// (one byte per rune, 0.25 tokens each => len/4).
	weightASCIIAlnum = 0.25
	// weightASCIIOther covers ASCII punctuation, whitespace, and symbols. Real
	// tokenizers often emit a separate token for punctuation runs and leading
	// spaces, so these cost a little more per rune than alphanumerics.
	weightASCIIOther = 0.30
	// weightCJK covers wide East Asian scripts (Han, Hiragana, Katakana,
	// Hangul). Each such rune is its own token (often more) in modern BPE
	// vocabularies, so it weighs a full token. This is the category where
	// bytes/4 underestimates the most.
	weightCJK = 1.0
	// weightOther covers remaining non-ASCII runes (accented Latin, Cyrillic,
	// Greek, emoji, and other multi-byte text). These typically tokenize at
	// well under one token per rune but above the ASCII rate, so they sit
	// between the two.
	weightOther = 0.50
)

// EstimateTokens returns a heuristic token count for text. It is deterministic,
// allocation-light, and dependency-free: no tokenizer vocabulary is consulted.
//
// The estimate is a rune-classified refinement of the agent's historical
// bytes/4 heuristic. On pure ASCII letters and digits it reproduces bytes/4
// exactly; on spaced or punctuated English it runs at or just above bytes/4
// (whitespace and punctuation carry a small premium), staying conservative
// rather than undercounting. The real accuracy gain is on text containing CJK
// or other multi-byte scripts, where it returns a higher, more realistic count
// because those runes cost close to a full token each despite their byte
// length, while bytes/4 structurally undercounts them. An empty string costs
// zero tokens; any non-empty string costs at least one.
//
// The result is monotonic in text: every rune contributes a strictly positive
// amount, so a longer string (in the append sense) never estimates fewer tokens
// than a prefix of it.
func EstimateTokens(text string) int {
	if text == "" {
		return 0
	}

	var sum float64
	for _, r := range text {
		switch {
		case r < utf8.RuneSelf: // ASCII (0x00-0x7F), including DEL.
			if isASCIIAlnum(r) {
				sum += weightASCIIAlnum
			} else {
				sum += weightASCIIOther
			}
		case isCJK(r):
			sum += weightCJK
		default:
			sum += weightOther
		}
	}

	n := int(sum)
	if n < 1 {
		return 1
	}
	return n
}

// messageStructuralTokens is the per-message framing overhead a provider adds
// around the content of a single message (role markers, message delimiters).
// Real chat formats spend a handful of tokens per message on this scaffolding;
// charging a small flat amount keeps multi-message budgets from underestimating
// without pretending to model any one provider's exact framing.
const messageStructuralTokens = 4

// imageBlockTokens is the flat cost charged for an inline image. Providers bill
// images by tiled area, which is not recoverable from the block (it carries
// only the encoded bytes, not the decoded dimensions), so a conservative flat
// estimate is used rather than scaling by the base64 length, which would wildly
// overcount.
const imageBlockTokens = 256

// EstimateMessageTokens returns a heuristic token count for a single message,
// summing the estimated tokens of each content block plus a small flat
// per-message framing overhead. It is deterministic and dependency-free.
//
// Per block:
//   - TextBlock and ThinkingBlock contribute EstimateTokens of their text.
//   - ToolResultBlock contributes EstimateTokens of its content.
//   - ToolUseBlock contributes EstimateTokens of its name plus its raw input
//     JSON, since both are serialized onto the wire.
//   - ImageBlock contributes a flat imageBlockTokens (see that constant for
//     why the byte length is not used).
//
// Unknown block types contribute only the structural overhead.
func EstimateMessageTokens(msg message.Message) int {
	total := messageStructuralTokens
	for _, block := range msg.Content {
		switch b := block.(type) {
		case message.TextBlock:
			total += EstimateTokens(b.Text)
		case message.ThinkingBlock:
			total += EstimateTokens(b.Text)
		case message.ToolResultBlock:
			total += EstimateTokens(b.Content)
		case message.ToolUseBlock:
			total += EstimateTokens(b.Name)
			total += EstimateTokens(string(b.Input))
		case message.ImageBlock:
			total += imageBlockTokens
		}
	}
	return total
}

// TokenCounter is an optional capability a Provider may implement when it has a
// real server-side token-counting endpoint (for example Anthropic's
// /v1/messages/count_tokens). Callers should type-assert a Provider to
// TokenCounter and fall back to EstimateMessageTokens when the assertion fails
// or the call errors, so budget math degrades gracefully without a network.
type TokenCounter interface {
	// CountTokens returns the provider-reported input token count for req. It
	// performs a network round trip and so may fail; callers should treat a
	// non-nil error as a signal to fall back to the local estimate.
	CountTokens(ctx context.Context, req Request) (int, error)
}

func isASCIIAlnum(r rune) bool {
	return (r >= '0' && r <= '9') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= 'a' && r <= 'z')
}

// isCJK reports whether r belongs to a wide East Asian script whose characters
// each cost roughly a whole token. It covers the common CJK, Japanese kana, and
// Korean Hangul ranges; it is intentionally a coarse classifier rather than an
// exhaustive Unicode block table.
func isCJK(r rune) bool {
	switch {
	case r >= 0x4E00 && r <= 0x9FFF: // CJK Unified Ideographs.
		return true
	case r >= 0x3400 && r <= 0x4DBF: // CJK Extension A.
		return true
	case r >= 0x3040 && r <= 0x30FF: // Hiragana and Katakana.
		return true
	case r >= 0xAC00 && r <= 0xD7A3: // Hangul syllables.
		return true
	case r >= 0xF900 && r <= 0xFAFF: // CJK Compatibility Ideographs.
		return true
	default:
		return false
	}
}
