package tui

import (
	"sort"
	"strings"
	"unicode"
)

// Scored fuzzy matching for the command palette, @-file picker, and the
// autocomplete providers. Unlike the older three-tier (prefix → substring →
// subsequence) ranking, this assigns each candidate a single numeric score so a
// match's quality — not just its band — orders the results: a query that lands
// on word boundaries ("sb" → "status_bar.go") or runs contiguously outranks one
// scattered loosely across the name, while gaps between matched runes are
// penalized so a tight match floats to the top. The weights below are the only
// tuning knobs; they are deliberately coarse so the ordering reads as obvious
// rather than finely-balanced.
const (
	// fuzzyScoreMatch is the base reward for every matched rune, large enough
	// that matching more of the query always beats matching less of it.
	fuzzyScoreMatch = 16
	// fuzzyBonusBoundary rewards a rune matched at a word boundary — the first
	// rune, or one immediately after a separator (space, /, ., _, -). It is large
	// relative to the gap penalty so an acronym ("sb") lighting the segment
	// initials of "status_bar.go" outscores a near-contiguous mid-word hit, the
	// way a good fuzzy finder favors word-start matches.
	fuzzyBonusBoundary = 14
	// fuzzyBonusCamel rewards a rune matched at a camelCase hump (a lower→upper
	// transition), so "ps" finds "parseStatus" on its word starts even without a
	// separator between the words.
	fuzzyBonusCamel = 10
	// fuzzyBonusConsecutive rewards a rune matched immediately after the previous
	// match, so a contiguous run scores higher than the same runes spread out —
	// the contiguity preference fuzzy finders and editor pickers share.
	fuzzyBonusConsecutive = 10
	// fuzzyPenaltyGap is charged per skipped target rune between two matches (and
	// for the leading skip before the first match), so a tighter span outranks a
	// looser one. It is kept small relative to the boundary bonus so a long but
	// boundary-aligned acronym is not buried under its gaps.
	fuzzyPenaltyGap = 2
	// fuzzyMaxLen caps the target length the quadratic match scans, so a
	// pathologically long candidate (a minified line pasted as a path) cannot
	// make the picker quadratic-blow-up. Beyond the cap the candidate is matched
	// on its first fuzzyMaxLen runes, which is ample for any real command or path.
	fuzzyMaxLen = 512
)

// fuzzyResult pairs a candidate with its score and the rune positions that
// matched, so callers can both rank (by score) and highlight (by positions) from
// one pass.
type fuzzyResult struct {
	// Index is the candidate's position in the slice passed to fuzzyRank, so the
	// caller can map a result back to its original entry (and any parallel
	// metadata) without a second lookup.
	Index int
	// Score is the match quality; higher ranks first.
	Score int
	// Positions are the rune indices of the candidate that matched the query, in
	// ascending order, for per-rune highlighting.
	Positions []int
}

// fuzzyMatch scores how well query matches target, case-insensitively, reporting
// ok only when every rune of query appears in target in order (a subsequence).
// An empty query matches any target with score 0 and no positions, so a bare
// trigger (a lone "/" or "@") lists everything unranked. The returned positions
// are the target rune indices that matched, best-scoring assignment first, so a
// query that can land on word boundaries is highlighted there rather than on its
// earliest possible runes.
//
// It runs a small dynamic program over (query rune × target rune): best[i][p] is
// the highest score for matching query[:i+1] with query[i] landing on target
// rune p, carried forward from the best earlier match minus the gap crossed to
// reach p (or plus the consecutive bonus when p adjoins the previous match). The
// optimum over the last query rune is the score; a parent table reconstructs the
// positions. This is quadratic in the (short) candidate length, bounded by
// fuzzyMaxLen.
func fuzzyMatch(query, target string) (score int, positions []int, ok bool) {
	q := []rune(strings.ToLower(query))
	if len(q) == 0 {
		return 0, nil, true
	}
	tr := []rune(target)
	if len(tr) > fuzzyMaxLen {
		tr = tr[:fuzzyMaxLen]
	}
	tl := make([]rune, len(tr))
	for i, r := range tr {
		tl[i] = unicode.ToLower(r)
	}
	if len(q) > len(tr) {
		return 0, nil, false
	}

	const negInf = -1 << 30
	// best[i][p] and parent[i][p] are flattened into 1-D rows reused per query
	// rune; we keep the full grid so positions can be reconstructed at the end.
	best := make([][]int, len(q))
	parent := make([][]int, len(q))
	for i := range best {
		best[i] = make([]int, len(tr))
		parent[i] = make([]int, len(tr))
		for p := range best[i] {
			best[i][p] = negInf
			parent[i][p] = -1
		}
	}

	for p := 0; p < len(tr); p++ {
		if tl[p] != q[0] {
			continue
		}
		// First query rune: base reward and boundary bonus, less a gap penalty for
		// however far into the target the match sits (a leading match is best).
		best[0][p] = fuzzyScoreMatch + boundaryBonus(tr, p) - fuzzyPenaltyGap*p
	}

	for i := 1; i < len(q); i++ {
		// runningBest tracks the best best[i-1][pp] seen for pp < p, decayed by the
		// per-rune gap penalty as p advances, so the inner max is amortized O(1)
		// and the whole DP stays O(query × target).
		runningBest := negInf
		runningAt := -1
		for p := 0; p < len(tr); p++ {
			// Fold the previous column (pp == p-1) into the running best before using
			// it, decaying earlier candidates by one gap step for the rune now skipped.
			if runningBest > negInf {
				runningBest -= fuzzyPenaltyGap
			}
			if p > 0 && best[i-1][p-1] > runningBest {
				// A match at p-1 reaches p as a consecutive pair: no gap, plus bonus.
				if cand := best[i-1][p-1] + fuzzyBonusConsecutive; cand > runningBest {
					runningBest = cand
					runningAt = p - 1
				}
			}
			if tl[p] == q[i] && runningBest > negInf {
				best[i][p] = runningBest + fuzzyScoreMatch + boundaryBonus(tr, p)
				parent[i][p] = runningAt
			}
		}
	}

	// The score is the best assignment ending the last query rune anywhere.
	last := len(q) - 1
	bestP, bestScore := -1, negInf
	for p := 0; p < len(tr); p++ {
		if best[last][p] > bestScore {
			bestScore = best[last][p]
			bestP = p
		}
	}
	if bestP < 0 {
		return 0, nil, false
	}

	positions = make([]int, len(q))
	p := bestP
	for i := last; i >= 0; i-- {
		positions[i] = p
		p = parent[i][p]
		if p < 0 && i > 0 {
			// Defensive: a broken parent chain should not happen for a real match,
			// but never index a negative position if it does.
			return bestScore, positions[i:], true
		}
	}
	return bestScore, positions, true
}

// boundaryBonus returns the word-boundary or camelCase reward for matching the
// rune at index p of target, or zero when p sits mid-word. The first rune and
// any rune following a separator (space, /, ., _, -) earn the full boundary
// bonus; a lower→upper transition earns the smaller camel bonus.
func boundaryBonus(target []rune, p int) int {
	if p == 0 {
		return fuzzyBonusBoundary
	}
	prev := target[p-1]
	if isFuzzySeparator(prev) {
		return fuzzyBonusBoundary
	}
	if unicode.IsLower(prev) && unicode.IsUpper(target[p]) {
		return fuzzyBonusCamel
	}
	return 0
}

// isFuzzySeparator reports whether r delimits a word for boundary scoring. The
// set mirrors wordStartIndices' separators so acronym matching and boundary
// rewards agree on where a "word" begins.
func isFuzzySeparator(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '/', '.', '_', '-':
		return true
	default:
		return false
	}
}

// fuzzyRank scores every candidate against query and returns the matches,
// best-first. A non-matching candidate is dropped. Ties (equal score) are broken
// toward the shorter candidate, then by the original order so the ranking is
// stable. An empty query returns every candidate in original order with a zero
// score, so a bare trigger lists everything without reordering.
func fuzzyRank(query string, candidates []string) []fuzzyResult {
	out := make([]fuzzyResult, 0, len(candidates))
	for i, c := range candidates {
		score, pos, ok := fuzzyMatch(query, c)
		if !ok {
			continue
		}
		out = append(out, fuzzyResult{Index: i, Score: score, Positions: pos})
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Score != out[b].Score {
			return out[a].Score > out[b].Score
		}
		ca, cb := candidates[out[a].Index], candidates[out[b].Index]
		if len(ca) != len(cb) {
			return len(ca) < len(cb)
		}
		return out[a].Index < out[b].Index
	})
	return out
}
