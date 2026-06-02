package agent

import (
	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/message"
)

// TurnComplexity classifies how demanding a single user turn is, so a Router
// can trade model strength against cost. It is derived from cheap, deterministic
// signals available before the provider is called (prompt length, whether tools
// are in play, an explicit caller hint).
type TurnComplexity int

const (
	// ComplexityUnset means no explicit signal was provided; a Router decides
	// using its own heuristics over the turn.
	ComplexityUnset TurnComplexity = iota
	// ComplexitySimple marks a short, tool-free turn that a cheaper model can
	// handle, e.g. a one-line question or a quick edit instruction.
	ComplexitySimple
	// ComplexityComplex marks a long or tool-driven turn that warrants a
	// stronger (and typically pricier) model.
	ComplexityComplex
)

// Turn bundles the per-turn signals a Router inspects to pick a model. It is
// assembled once at the start of a Run, before the provider is called, so model
// selection is deterministic for the whole turn.
type Turn struct {
	// History is the conversation sent to the provider for this turn, most
	// recent message last. It includes the just-appended user message.
	History []message.Message
	// ToolsAvailable reports whether any tools are offered to the model on this
	// turn. A tool-enabled turn is more likely to require multi-step reasoning.
	ToolsAvailable bool
	// Hint is an explicit complexity override supplied by the caller. When it is
	// not ComplexityUnset, a Router should honor it rather than re-deriving the
	// complexity from the prompt.
	Hint TurnComplexity
}

// Router selects which configured model to use for a turn. Implementations
// receive the per-turn signals and the models the active provider exposes, and
// return the ID of the model to use. Returning an empty string (or an ID not in
// models) leaves the Loop's configured default model in place, so a Router can
// decline to route a given turn without breaking it.
//
// A nil Router means no routing: the Loop always uses its configured model,
// which is the default, non-breaking behavior.
type Router interface {
	// Route returns the chosen model ID for the turn, or "" to keep the default.
	Route(turn Turn, models []llm.Model) string
}

// CostAwareRouter routes simple/short turns to the cheapest configured model and
// complex/long or tool-driven turns to the strongest one, where "strongest" is
// approximated by price. It is the default cost-aware policy and is opt-in: it
// only takes effect when set on a Loop's Config.
//
// Cost comes from the model price metadata exposed by the llm package
// (InputPricePerMTokUSD / OutputPricePerMTokUSD). When that metadata is absent
// (all zero), the router falls back to model order as a coarse proxy: the
// provider's first model is treated as the cheaper one and its last as the
// stronger one. See the config followup in the package notes.
type CostAwareRouter struct {
	// PromptLenThreshold is the user-prompt character count at or above which a
	// turn is treated as complex when no explicit hint is set. A non-positive
	// value selects defaultPromptLenThreshold.
	PromptLenThreshold int
	// ToolsImplyComplex, when true, treats any tool-enabled turn as complex even
	// for a short prompt, on the theory that tool use signals multi-step work.
	ToolsImplyComplex bool
}

// defaultPromptLenThreshold is the prompt length (in characters of the latest
// user message) at or above which CostAwareRouter treats an unhinted turn as
// complex.
const defaultPromptLenThreshold = 280

// Route implements Router. It classifies the turn, then picks the cheapest or
// strongest eligible model accordingly. It returns "" when it cannot make a
// meaningful choice (fewer than two eligible models), leaving the default model
// in place.
func (r CostAwareRouter) Route(turn Turn, models []llm.Model) string {
	eligible := routableModels(models)
	if len(eligible) < 2 {
		return ""
	}
	cheap, strong := cheapestAndStrongest(eligible)
	if r.classify(turn) == ComplexityComplex {
		return strong.ID
	}
	return cheap.ID
}

// classify maps a turn to a complexity. An explicit, non-unset hint always wins.
// Otherwise the turn is complex when the latest user prompt is long, or when
// tools are available and ToolsImplyComplex is set.
func (r CostAwareRouter) classify(turn Turn) TurnComplexity {
	if turn.Hint != ComplexityUnset {
		return turn.Hint
	}
	if r.ToolsImplyComplex && turn.ToolsAvailable {
		return ComplexityComplex
	}
	threshold := r.PromptLenThreshold
	if threshold <= 0 {
		threshold = defaultPromptLenThreshold
	}
	if latestUserPromptLen(turn.History) >= threshold {
		return ComplexityComplex
	}
	return ComplexitySimple
}

// routableModels drops models that cannot serve a turn (pending releases) so the
// router never routes to a model the provider will not actually run. Order is
// preserved so the order-based fallback stays deterministic.
func routableModels(models []llm.Model) []llm.Model {
	out := make([]llm.Model, 0, len(models))
	for _, m := range models {
		if m.Pending {
			continue
		}
		out = append(out, m)
	}
	return out
}

// cheapestAndStrongest returns the cheapest and the strongest model from
// eligible, ranked by blended price. When no price metadata is present (all
// prices zero), it falls back to model order: the first model is the cheaper one
// and the last is the stronger one. eligible must hold at least one model.
func cheapestAndStrongest(eligible []llm.Model) (cheap, strong llm.Model) {
	if anyPriced(eligible) {
		cheap, strong = eligible[0], eligible[0]
		for _, m := range eligible[1:] {
			if modelPrice(m) < modelPrice(cheap) {
				cheap = m
			}
			if modelPrice(m) > modelPrice(strong) {
				strong = m
			}
		}
		return cheap, strong
	}
	// Unpriced fallback: first is cheaper, last is stronger by configured order.
	return eligible[0], eligible[len(eligible)-1]
}

// anyPriced reports whether any model carries non-zero price metadata, so the
// router can decide between price-based and order-based ranking.
func anyPriced(models []llm.Model) bool {
	for _, m := range models {
		if modelPrice(m) > 0 {
			return true
		}
	}
	return false
}

// modelPrice is the blended per-MTok cost used to rank models. Summing the input
// and output prices gives a single, stable scalar that orders cheap models below
// expensive ones without needing to know the input/output token mix in advance.
func modelPrice(m llm.Model) float64 {
	return m.InputPricePerMTokUSD + m.OutputPricePerMTokUSD
}

// latestUserPromptLen returns the character length of the text in the most
// recent genuine user message in history, or 0 when there is none. Tool-result
// messages (also RoleUser) are skipped so a large tool payload does not inflate
// the perceived prompt size.
func latestUserPromptLen(history []message.Message) int {
	if idx := latestUserIndex(history); idx >= 0 {
		n := 0
		for _, block := range history[idx].Content {
			if text, ok := block.(message.TextBlock); ok {
				n += len(text.Text)
			}
		}
		return n
	}
	return 0
}
