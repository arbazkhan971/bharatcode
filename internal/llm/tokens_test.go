package llm

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/arbazkhan971/bharatcode/internal/message"
)

// naiveBytes4 is the heuristic the agent uses today (bytes/4), reproduced here
// so the tests can assert EstimateTokens beats it on a representative string.
func naiveBytes4(s string) int {
	if s == "" {
		return 0
	}
	n := len(s) / 4
	if n < 1 {
		return 1
	}
	return n
}

func TestEstimateTokens_EmptyAndFloor(t *testing.T) {
	require.Equal(t, 0, EstimateTokens(""), "empty string costs zero tokens")
	// A single short rune rounds down to 0 weight but floors to 1 for any
	// non-empty input.
	require.Equal(t, 1, EstimateTokens("a"))
	require.Equal(t, 1, EstimateTokens("."))
}

func TestEstimateTokens_ASCIIAlnumMatchesBytes4Baseline(t *testing.T) {
	// On pure ASCII alphanumerics the rune-classified estimate reproduces the
	// bytes/4 baseline exactly: 0.25 tokens/char * 1 byte/char == len/4. This is
	// the no-regression-on-English guarantee. (Whitespace and punctuation weigh
	// slightly more than 0.25, so spaced sentences run at or just above bytes/4;
	// that case is covered by TestEstimateTokens_ASCIISentenceNoRegression.)
	for _, s := range []string{
		"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ",
		"estimateTokens0123456789returnsAnIntegerCount",
	} {
		require.Equal(t, naiveBytes4(s), EstimateTokens(s),
			"ASCII-alphanumeric string %q should match bytes/4 baseline", s)
	}
}

func TestEstimateTokens_ASCIISentenceNoRegression(t *testing.T) {
	// Spaced/punctuated English never estimates below bytes/4 (so budget math
	// stays conservative), staying within one token of it: whitespace and
	// punctuation weigh 0.30 rather than 0.25, a small documented premium.
	for _, s := range []string{
		"the quick brown fox jumps over the lazy dog",
		"function estimateTokens returns an integer count",
	} {
		got, naive := EstimateTokens(s), naiveBytes4(s)
		require.GreaterOrEqual(t, got, naive,
			"spaced ASCII %q should not estimate below bytes/4", s)
		require.LessOrEqual(t, got-naive, naive/8+2,
			"spaced ASCII %q should stay close to bytes/4 (got %d, naive %d)", s, got, naive)
	}
}

func TestEstimateTokens_KnownRanges(t *testing.T) {
	// EstimateTokens lands in a sane, defensible range for known strings.
	tests := []struct {
		name   string
		text   string
		minTok int
		maxTok int
	}{
		{
			// 43 ASCII chars, ~9 whitespace-separated words. cl100k tokenizes
			// this sentence at 9 tokens; our estimate should be in that
			// neighborhood, not off by an order of magnitude.
			name:   "english sentence",
			text:   "the quick brown fox jumps over the lazy dog",
			minTok: 8,
			maxTok: 14,
		},
		{
			// 5 Han characters. Real tokenizers spend ~1-2 tokens each, so a
			// count of 5-12 is sane; the key point (tested separately) is it
			// beats bytes/4, which would say 3.
			name:   "cjk phrase",
			text:   "你好世界吗",
			minTok: 5,
			maxTok: 12,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EstimateTokens(tt.text)
			require.GreaterOrEqual(t, got, tt.minTok,
				"estimate %d below sane floor for %q", got, tt.text)
			require.LessOrEqual(t, got, tt.maxTok,
				"estimate %d above sane ceiling for %q", got, tt.text)
		})
	}
}

func TestEstimateTokens_MonotonicInLength(t *testing.T) {
	// Appending text must never lower the estimate, because every rune
	// contributes a strictly positive weight. Build up a mixed string rune by
	// rune and assert the count is non-decreasing the whole way.
	const mixed = "Hello, 世界! "
	prev := 0
	runes := []rune(mixed)
	for i := 1; i <= len(runes); i++ {
		got := EstimateTokens(string(runes[:i]))
		require.GreaterOrEqual(t, got, prev,
			"estimate dropped after appending rune %d of %q", i, mixed)
		prev = got
	}
}

func TestEstimateTokens_BeatsNaiveOnMixedString(t *testing.T) {
	// Representative CJK-dominant string (short English label + 13 ideographs),
	// used to document the comparison against the naive bytes/4 heuristic.
	//
	// The accuracy win is structural, not dependent on a fabricated reference.
	// bytes/4 charges each CJK character at 3 UTF-8 bytes / 4 = 0.75 tokens, but
	// every CJK ideograph costs at least one whole token in cl100k/o200k-style
	// vocabularies. So a hard lower bound L on the true token count is:
	//
	//	L = (CJK char count, each >= 1 token) + (conservative English-word floor)
	//
	// For this string: 13 CJK chars + 2 for the "region:" label = L = 15.
	//
	// bytes/4 sits below L (it undercounts the ideographs); our estimate meets
	// L. The single inequality that proves our estimate is strictly closer to
	// the true count for EVERY true value >= L is:
	//
	//	2*L > naive + estimate      (i.e. L is above the midpoint of the two)
	//
	// When that holds, |estimate - true| < |naive - true| for all true >= L,
	// with no guessed upper bound and no per-reference fluke.
	const mixed = "region: 北京数据中心服务器集群节点"
	const cjkChars = 13
	const englishFloor = 2
	const trueFloorL = cjkChars + englishFloor // = 15.

	estimate := EstimateTokens(mixed)
	naive := naiveBytes4(mixed)

	// Precondition: bytes/4 really does undercount below the true floor.
	require.Less(t, naive, trueFloorL,
		"precondition: bytes/4 (%d) should sit below true floor L=%d", naive, trueFloorL)
	// Our estimate meets the floor (it is not itself an undercount below L).
	require.GreaterOrEqual(t, estimate, trueFloorL,
		"estimate (%d) should meet true floor L=%d", estimate, trueFloorL)

	// The structural win: L above the midpoint => estimate strictly closer than
	// bytes/4 for every true count at or above L.
	require.Greater(t, 2*trueFloorL, naive+estimate,
		"L=%d must exceed midpoint of naive=%d and estimate=%d so the estimate wins for all true>=L",
		trueFloorL, naive, estimate)

	// Concretely confirm the closer-for-all-true-values claim across a wide,
	// open-ended band starting at the floor.
	for ref := trueFloorL; ref <= trueFloorL+15; ref++ {
		require.Less(t, absInt(estimate-ref), absInt(naive-ref),
			"at true count %d: EstimateTokens (%d) should beat bytes/4 (%d)",
			ref, estimate, naive)
	}

	t.Logf("mixed=%q estimate=%d naive_bytes4=%d L=%d", mixed, estimate, naive, trueFloorL)
}

func TestEstimateMessageTokens_SumsBlocksPlusOverhead(t *testing.T) {
	input := json.RawMessage(`{"path":"/etc/hosts"}`)
	msg := message.Message{
		Role: message.RoleAssistant,
		Content: []message.ContentBlock{
			message.TextBlock{Text: "Reading the file now."},
			message.ToolUseBlock{Name: "read_file", Input: input},
			message.ThinkingBlock{Text: "I should open it."},
		},
	}

	want := messageStructuralTokens
	want += EstimateTokens("Reading the file now.")
	want += EstimateTokens("read_file") + EstimateTokens(string(input))
	want += EstimateTokens("I should open it.")

	require.Equal(t, want, EstimateMessageTokens(msg))
}

func TestEstimateMessageTokens_EmptyMessageIsOverheadOnly(t *testing.T) {
	require.Equal(t, messageStructuralTokens,
		EstimateMessageTokens(message.Message{Role: message.RoleUser}))
}

func TestEstimateMessageTokens_ImageIsFlatCost(t *testing.T) {
	// The image cost is flat and independent of the encoded byte length, so two
	// images of very different sizes contribute identically.
	small := message.Message{Content: []message.ContentBlock{
		message.ImageBlock{MimeType: "image/png", Data: []byte{0x1, 0x2}},
	}}
	large := message.Message{Content: []message.ContentBlock{
		message.ImageBlock{MimeType: "image/png", Data: make([]byte, 100_000)},
	}}
	require.Equal(t, messageStructuralTokens+imageBlockTokens, EstimateMessageTokens(small))
	require.Equal(t, EstimateMessageTokens(small), EstimateMessageTokens(large))
}

func TestEstimateMessageTokens_MonotonicAcrossBlocks(t *testing.T) {
	// Adding a block can only raise a message's estimate.
	base := message.Message{Content: []message.ContentBlock{
		message.TextBlock{Text: "first block of text"},
	}}
	more := message.Message{Content: append(
		append([]message.ContentBlock(nil), base.Content...),
		message.TextBlock{Text: "second block of text"},
	)}
	require.Greater(t, EstimateMessageTokens(more), EstimateMessageTokens(base))
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
