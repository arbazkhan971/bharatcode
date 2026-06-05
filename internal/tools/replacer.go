package tools

import "strings"

// This file implements whitespace-tolerant fallback matching for the edit and
// multiedit tools. Exact byte-for-byte matching remains the primary path: a
// fallback is only attempted when the exact old_string is absent from the file,
// and only for multi-line blocks (>= 2 lines), which is where indentation and
// trailing-whitespace drift most often defeats an otherwise-correct edit.
// Single-line mismatches deliberately stay strict so the caller can surface a
// whitespace hint and ask the model to re-view, matching BharatCode's existing
// edit philosophy.
//
// Every fallback still demands an unambiguous match: like exact matching, a
// block that maps to more than one location in the file is rejected unless
// replace_all is set. The strategy that matched is reported back to the model so
// a flexible application is never silent.

// replaceStatus enumerates the outcome of attempting a single replacement.
type replaceStatus int

const (
	// replaceOK means the edit was applied; result.text holds the new content.
	replaceOK replaceStatus = iota
	// replaceNotFound means neither exact nor flexible matching located old.
	replaceNotFound
	// replaceAmbiguous means a match was found in more than one place and
	// replace_all was not set; result.found carries the occurrence count.
	replaceAmbiguous
)

// replaceResult carries the outcome of applyReplacement.
type replaceResult struct {
	status   replaceStatus
	text     string // rewritten file content (valid when status == replaceOK)
	count    int    // replacements made, for the user-facing report
	found    int    // occurrences located (used for the ambiguity message)
	strategy string // "" for an exact match, else the flexible strategy name
}

// applyReplacement performs one edit against source. It first attempts exact
// matching (preserving the existing unique/duplicate semantics) and, only when
// the exact old_string is absent, falls back to whitespace-tolerant strategies
// for multi-line blocks.
func applyReplacement(source, old, replacement string, replaceAll bool) replaceResult {
	if count := strings.Count(source, old); count > 0 {
		if count > 1 && !replaceAll {
			return replaceResult{status: replaceAmbiguous, found: count}
		}
		n := 1
		if replaceAll {
			n = -1
		}
		return replaceResult{
			status: replaceOK,
			text:   strings.Replace(source, old, replacement, n),
			count:  countForReport(count, replaceAll),
		}
	}

	spans, strategy := findFlexibleSpans(source, old)
	if len(spans) == 0 {
		return replaceResult{status: replaceNotFound}
	}
	if len(spans) > 1 && !replaceAll {
		return replaceResult{status: replaceAmbiguous, found: len(spans)}
	}
	use := spans
	if !replaceAll {
		use = spans[:1]
	}
	return replaceResult{
		status:   replaceOK,
		text:     replaceSpans(source, use, replacement),
		count:    countForReport(len(spans), replaceAll),
		strategy: strategy,
	}
}

// span is a half-open byte range [start, end) within the source text.
type span struct {
	start int
	end   int
}

// srcLine records the byte range of a single source line, excluding its
// trailing newline.
type srcLine struct {
	start int
	end   int
}

// splitLineOffsets returns the byte range of each line in s. A trailing newline
// yields a final empty line, mirroring strings.Split semantics so blank-line
// alignment is preserved during comparison.
func splitLineOffsets(s string) []srcLine {
	lines := make([]srcLine, 0, strings.Count(s, "\n")+1)
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, srcLine{start: start, end: i})
			start = i + 1
		}
	}
	lines = append(lines, srcLine{start: start, end: len(s)})
	return lines
}

// findFlexibleSpans locates byte spans in source that correspond to old using
// progressively looser, whitespace-tolerant strategies. It returns the matching
// spans (in ascending, non-overlapping order) and the name of the strategy that
// produced them, or (nil, "") when no confident multi-line match exists.
func findFlexibleSpans(source, old string) ([]span, string) {
	oldLines := strings.Split(old, "\n")
	// Drop the single trailing empty element produced when old ends with "\n";
	// it would otherwise demand a matching blank line at the block's tail.
	if len(oldLines) > 1 && oldLines[len(oldLines)-1] == "" {
		oldLines = oldLines[:len(oldLines)-1]
	}
	// Single-line blocks stay strict (see file comment).
	if len(oldLines) < 2 {
		return nil, ""
	}
	srcLines := splitLineOffsets(source)

	// Strategy 1: line-trimmed — tolerant of leading/trailing whitespace and
	// indentation, but otherwise requires each line to match exactly.
	if spans := matchLineBlocks(source, srcLines, oldLines, strings.TrimSpace); len(spans) > 0 {
		return spans, "line-trimmed"
	}
	// Strategy 2: whitespace-normalized — additionally collapses internal runs
	// of whitespace, catching reflowed spacing inside a line.
	if spans := matchLineBlocks(source, srcLines, oldLines, normalizeWhitespace); len(spans) > 0 {
		return spans, "whitespace-normalized"
	}
	// Strategy 3: block-anchor — for blocks of >= 3 lines, anchor on the unique
	// first and last trimmed lines and span the region between them, tolerating
	// differences in the interior lines.
	if len(oldLines) >= 3 {
		if spans := matchBlockAnchor(source, srcLines, oldLines); len(spans) > 0 {
			return spans, "block-anchor"
		}
	}
	return nil, ""
}

// matchLineBlocks scans srcLines for non-overlapping windows whose lines all
// equal the corresponding old line under norm. Each returned span covers from
// the first matched line's start to the last matched line's end (excluding the
// trailing newline), so applying the replacement preserves surrounding line
// breaks.
func matchLineBlocks(source string, srcLines []srcLine, oldLines []string, norm func(string) string) []span {
	n := len(oldLines)
	if n == 0 || n > len(srcLines) {
		return nil
	}
	normOld := make([]string, n)
	for i, l := range oldLines {
		normOld[i] = norm(l)
	}
	var spans []span
	for i := 0; i+n <= len(srcLines); {
		match := true
		for j := 0; j < n; j++ {
			if norm(source[srcLines[i+j].start:srcLines[i+j].end]) != normOld[j] {
				match = false
				break
			}
		}
		if match {
			spans = append(spans, span{start: srcLines[i].start, end: srcLines[i+n-1].end})
			i += n // non-overlapping
			continue
		}
		i++
	}
	return spans
}

// matchBlockAnchor matches a multi-line block by its first and last trimmed
// lines. To stay unambiguous it requires each anchor to occur exactly once in
// the file; the span then covers everything between (and including) the two
// anchor lines.
func matchBlockAnchor(source string, srcLines []srcLine, oldLines []string) []span {
	first := strings.TrimSpace(oldLines[0])
	last := strings.TrimSpace(oldLines[len(oldLines)-1])
	if first == "" || last == "" {
		return nil
	}
	firstIdx, firstCount := -1, 0
	lastIdx, lastCount := -1, 0
	for i, l := range srcLines {
		t := strings.TrimSpace(source[l.start:l.end])
		if t == first {
			firstCount++
			if firstIdx == -1 {
				firstIdx = i
			}
		}
		if t == last {
			lastCount++
			lastIdx = i
		}
	}
	if firstCount != 1 || lastCount != 1 || lastIdx <= firstIdx {
		return nil
	}
	return []span{{start: srcLines[firstIdx].start, end: srcLines[lastIdx].end}}
}

// replaceSpans rewrites source by substituting replacement for each span. Spans
// must be ascending and non-overlapping.
func replaceSpans(source string, spans []span, replacement string) string {
	var b strings.Builder
	prev := 0
	for _, s := range spans {
		b.WriteString(source[prev:s.start])
		b.WriteString(replacement)
		prev = s.end
	}
	b.WriteString(source[prev:])
	return b.String()
}

// normalizeWhitespace collapses every run of whitespace to a single space and
// trims the ends, so lines differing only in spacing compare equal.
func normalizeWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
