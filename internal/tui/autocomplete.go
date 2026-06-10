package tui

import (
	"sort"
	"strings"
)

// AutocompleteProvider supplies ranked completions for one kind of prompt token.
// The prompt's completion is composed from several providers — slash commands,
// @-file references, and @-mentions — each of which knows how to recognize its
// own in-progress token in the buffer and how to rank candidates for it. A
// provider is consulted only when it claims the buffer via Match, so the composer
// never has to know the syntax of each token type; it just asks every provider
// whether the buffer ends in something it can complete.
type AutocompleteProvider interface {
	// Name identifies the provider for diagnostics and tests.
	Name() string
	// Match reports the in-progress token this provider would complete for buffer,
	// and the rune offset where a chosen replacement begins (so the composer can
	// splice a candidate's Value in over the typed token). ok is false when the
	// buffer holds nothing this provider recognizes.
	Match(buffer string) (token string, start int, ok bool)
	// Suggest ranks candidates for an already-recognized token. The token is the
	// text Match returned; an empty token requests the provider's default listing.
	Suggest(token string) []Candidate
}

// Candidate is one ranked completion. Value is what replaces the typed token in
// the buffer (including its marker, e.g. "/help" or "@main.go"); Display is the
// menu label; Detail is an optional gloss (a command description, a file size).
// Score and Positions come from the fuzzy matcher so the menu can both order and
// highlight by the same pass.
type Candidate struct {
	Value     string
	Display   string
	Detail    string
	Score     int
	Positions []int
}

// autocomplete composes a set of providers into a single completion source. For
// a given buffer it asks each provider in turn to Match; every provider that
// claims the same token contributes its candidates, which are merged and
// re-ranked by fuzzy score so, for example, typing "@a" can surface both files
// and named mentions interleaved by relevance rather than grouped by source.
type autocomplete struct {
	providers []AutocompleteProvider
}

// newAutocomplete builds a composer over the given providers, consulted in the
// order supplied (which breaks ties between equally-scored candidates from
// different providers).
func newAutocomplete(providers ...AutocompleteProvider) *autocomplete {
	return &autocomplete{providers: providers}
}

// suggest returns the merged, ranked completions for buffer and the rune offset
// where a chosen candidate's Value should be spliced in. It returns a nil slice
// and start -1 when no provider recognizes the buffer. When several providers
// match, the one that recognizes the longest token (the most specific syntax)
// sets the splice offset; providers that matched a shorter token are skipped so
// their candidates do not splice at the wrong place.
func (a *autocomplete) suggest(buffer string) (cands []Candidate, start int) {
	start = -1
	bestTokenLen := -1
	type claim struct {
		p     AutocompleteProvider
		token string
		start int
	}
	var claims []claim
	for _, p := range a.providers {
		token, s, ok := p.Match(buffer)
		if !ok {
			continue
		}
		claims = append(claims, claim{p: p, token: token, start: s})
		if l := len([]rune(token)); l > bestTokenLen || (l == bestTokenLen && s == start) {
			bestTokenLen = l
		}
	}
	if len(claims) == 0 {
		return nil, -1
	}
	// Anchor on the largest splice offset claimed: providers sharing it (the
	// @-file and @-mention providers, which point at the same "@") merge; a
	// provider claiming a different offset is for a different token and is dropped.
	for _, c := range claims {
		if c.start > start {
			start = c.start
		}
	}
	for _, c := range claims {
		if c.start != start {
			continue
		}
		cands = append(cands, c.p.Suggest(c.token)...)
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].Score != cands[j].Score {
			return cands[i].Score > cands[j].Score
		}
		if len(cands[i].Display) != len(cands[j].Display) {
			return len(cands[i].Display) < len(cands[j].Display)
		}
		return cands[i].Display < cands[j].Display
	})
	return cands, start
}

// --- slash-command provider ---

// slashProvider completes the built-in and dynamic slash commands. It is active
// only when the buffer is a single leading-slash word (no whitespace yet), so a
// "/help me with x" prose line — where the slash command is already chosen — is
// not re-completed.
type slashProvider struct {
	commands     []string
	descriptions map[string]string
}

// newSlashProvider builds a slash provider over a command set and an optional
// description map keyed by "/name".
func newSlashProvider(commands []string, descriptions map[string]string) *slashProvider {
	return &slashProvider{commands: commands, descriptions: descriptions}
}

func (p *slashProvider) Name() string { return "slash" }

// Match claims a buffer that begins with "/" and contains no whitespace,
// returning the command word without its leading slash and a splice offset of 0
// (a chosen command replaces the whole buffer).
func (p *slashProvider) Match(buffer string) (token string, start int, ok bool) {
	if !strings.HasPrefix(buffer, "/") {
		return "", 0, false
	}
	if strings.ContainsAny(buffer, " \t\n") {
		return "", 0, false
	}
	return strings.TrimPrefix(buffer, "/"), 0, true
}

// Suggest ranks the commands against the typed token with the scored fuzzy
// matcher (the leading slash is excluded from both sides so "hl" finds "/help").
// An empty token lists every command in declared order.
func (p *slashProvider) Suggest(token string) []Candidate {
	names := make([]string, len(p.commands))
	for i, c := range p.commands {
		names[i] = strings.TrimPrefix(c, "/")
	}
	out := make([]Candidate, 0, len(names))
	if token == "" {
		for i, name := range names {
			out = append(out, Candidate{
				Value:   p.commands[i],
				Display: p.commands[i],
				Detail:  p.descriptions["/"+name],
			})
		}
		return out
	}
	for _, r := range fuzzyRank(token, names) {
		name := names[r.Index]
		out = append(out, Candidate{
			Value:     p.commands[r.Index],
			Display:   p.commands[r.Index],
			Detail:    p.descriptions["/"+name],
			Score:     r.Score,
			Positions: shiftPositions(r.Positions, 1), // account for the leading "/"
		})
	}
	return out
}

// --- @-file provider ---

// fileProvider completes @-file references against the workspace listing, honoring
// .gitignore so build output and vendored trees never surface. It shares
// activeMention's notion of an in-progress @-token so it claims exactly the
// buffers the existing @-file picker does.
type fileProvider struct {
	root    string
	matcher *gitignoreMatcher
	// files caches the gitignore-filtered listing so repeated keystrokes do not
	// re-walk the tree; it is populated lazily on first use.
	files  []string
	listed bool
}

// newFileProvider builds an @-file provider rooted at root. The gitignore matcher
// is built eagerly so the root .gitignore is read once.
func newFileProvider(root string) *fileProvider {
	return &fileProvider{root: root, matcher: newGitignoreMatcher(root)}
}

func (p *fileProvider) Name() string { return "file" }

// Match claims a buffer that ends in an in-progress @-token, returning the token
// (the text after "@") and the rune offset of the "@" so a chosen file replaces
// from there.
func (p *fileProvider) Match(buffer string) (token string, start int, ok bool) {
	token, ok = activeMention(buffer)
	if !ok {
		return "", 0, false
	}
	at := strings.LastIndex(buffer, "@")
	return token, len([]rune(buffer[:at])), true
}

// Suggest ranks the workspace files against the token with the scored fuzzy
// matcher; an empty token returns the head of the listing so a bare "@" reveals
// what is available. Each candidate's Value carries the "@" so it splices as a
// complete reference.
func (p *fileProvider) Suggest(token string) []Candidate {
	files := p.list()
	if token == "" {
		limit := maxMentionHints
		if limit > len(files) {
			limit = len(files)
		}
		out := make([]Candidate, 0, limit)
		for _, f := range files[:limit] {
			out = append(out, Candidate{Value: "@" + f, Display: f})
		}
		return out
	}
	ranked := fuzzyRank(token, files)
	if len(ranked) > maxMentionHints {
		ranked = ranked[:maxMentionHints]
	}
	out := make([]Candidate, 0, len(ranked))
	for _, r := range ranked {
		out = append(out, Candidate{
			Value:     "@" + files[r.Index],
			Display:   files[r.Index],
			Score:     r.Score,
			Positions: r.Positions,
		})
	}
	return out
}

// list returns the gitignore-filtered workspace listing, walking the tree once
// and caching the result for the lifetime of the provider.
func (p *fileProvider) list() []string {
	if !p.listed {
		p.files = listFilesGitignored(p.root, p.matcher)
		p.listed = true
	}
	return p.files
}

// --- @-mention provider ---

// mentionProvider completes @-mentions against a fixed name set — agents, named
// sessions, or any roster the host wires in — distinct from file references. It
// shares the @-token syntax with the file provider, so the composer merges both
// sets when the user types "@", ranking files and mentions together by relevance.
type mentionProvider struct {
	names []string
}

// newMentionProvider builds a mention provider over a roster of names (each
// completed as "@name").
func newMentionProvider(names []string) *mentionProvider {
	return &mentionProvider{names: names}
}

func (p *mentionProvider) Name() string { return "mention" }

// Match claims the same in-progress @-token the file provider does, so both
// contribute candidates for an "@" the user is typing.
func (p *mentionProvider) Match(buffer string) (token string, start int, ok bool) {
	token, ok = activeMention(buffer)
	if !ok {
		return "", 0, false
	}
	at := strings.LastIndex(buffer, "@")
	return token, len([]rune(buffer[:at])), true
}

// Suggest ranks the roster against the token; an empty token lists every name in
// roster order. Each Value carries the "@" so it splices as a complete mention.
func (p *mentionProvider) Suggest(token string) []Candidate {
	if token == "" {
		out := make([]Candidate, 0, len(p.names))
		for _, n := range p.names {
			out = append(out, Candidate{Value: "@" + n, Display: "@" + n})
		}
		return out
	}
	out := make([]Candidate, 0, len(p.names))
	for _, r := range fuzzyRank(token, p.names) {
		out = append(out, Candidate{
			Value:     "@" + p.names[r.Index],
			Display:   "@" + p.names[r.Index],
			Score:     r.Score,
			Positions: shiftPositions(r.Positions, 1), // account for the leading "@"
		})
	}
	return out
}

// shiftPositions returns a copy of pos with each index advanced by delta, used to
// re-base fuzzy match positions (computed on a name) onto a Display string that
// carries a leading marker rune ("/" or "@").
func shiftPositions(pos []int, delta int) []int {
	if len(pos) == 0 {
		return nil
	}
	out := make([]int, len(pos))
	for i, p := range pos {
		out[i] = p + delta
	}
	return out
}
