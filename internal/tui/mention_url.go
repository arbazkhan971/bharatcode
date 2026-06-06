package tui

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/tools"
	"github.com/arbazkhan971/bharatcode/internal/util"
)

// maxURLMentionBytes caps each fetched URL's content so one large page cannot
// dominate the context window. Matches maxMentionFileBytes for file mentions.
const maxURLMentionBytes = 100 * 1024

// urlMentionPattern matches an @URL reference at a mention boundary (start of
// input, whitespace, or an opening bracket) followed by http:// or https:// and
// the non-whitespace characters forming the URL. A separate pattern is needed
// because mentionPattern's path char class excludes ":" and "%".
var urlMentionPattern = regexp.MustCompile(`(^|[\s(\[{])@(https?://\S+)`)

// expandURLMentions scans text for @URL references (http:// or https:// prefixed),
// fetches each unique URL using the SSRF-safe web_fetch transport, and appends the
// retrieved content as an "[Attached URLs]" section. The original text is kept
// verbatim. It returns the expanded text and the list of successfully fetched URLs.
// Mentions whose fetch fails are left in the text untouched with no annotation.
//
// This must be called from a goroutine (not the Bubble Tea Update handler) because
// it performs network I/O. It is called from runAgent's closure in agentrun.go.
func expandURLMentions(ctx context.Context, text string) (string, []string) {
	matches := urlMentionPattern.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return text, nil
	}

	var fetched []string
	var blocks []string
	seen := make(map[string]bool)

	for _, m := range matches {
		rawURL := strings.TrimRight(m[2], mentionTrailingPunct)
		if rawURL == "" || seen[rawURL] {
			continue
		}
		seen[rawURL] = true

		content, truncated, err := tools.FetchPageText(ctx, rawURL, maxURLMentionBytes)
		if err != nil {
			continue // leave the @URL token in the text intact; don't annotate failures
		}
		fetched = append(fetched, rawURL)
		blocks = append(blocks, renderURLMentionBlock(rawURL, content, truncated))
	}

	if len(blocks) == 0 {
		return text, nil
	}

	var b strings.Builder
	b.WriteString(text)
	b.WriteString("\n\n[Attached URLs]\n")
	for _, blk := range blocks {
		b.WriteString(blk)
	}
	return b.String(), fetched
}

// renderURLMentionBlock formats a fetched URL as a labelled fenced block,
// mirroring renderMentionBlock for file attachments. A size annotation and
// optional truncation notice are appended so the model knows how much content
// was provided.
func renderURLMentionBlock(rawURL, content string, truncated bool) string {
	var b strings.Builder
	size := int64(len(content))
	fmt.Fprintf(&b, "@%s [%s]:\n```\n", rawURL, util.HumanBytes(size))
	b.WriteString(content)
	if len(content) > 0 && content[len(content)-1] != '\n' {
		b.WriteByte('\n')
	}
	if truncated {
		b.WriteString("… [truncated]\n")
	}
	b.WriteString("```\n")
	return b.String()
}
