package agent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// loopDetectorWindow bounds the ring buffer of recent tool observations the
// detector inspects. It must be large enough to hold the longest pattern the
// detector trips on (an A,B,A,B cycle) plus the identical-run threshold.
const loopDetectorWindow = 8

// identicalRunThreshold (K) is how many consecutive identical observations
// (same call AND same result AND same error flag) trip the guard. A run of K
// is the point at which retrying is judged futile.
const identicalRunThreshold = 3

// observation is one executed tool call together with the result it produced.
// Two observations are equal when the model issued the same call and the tool
// returned the same bytes with the same error status — the signal that a step
// made no progress and is being repeated verbatim.
type observation struct {
	callHash   string
	resultHash string
	isError    bool
}

func (o observation) equal(other observation) bool {
	return o.callHash == other.callHash &&
		o.resultHash == other.resultHash &&
		o.isError == other.isError
}

// loopDetector is a windowed, result-aware tool-loop guard. Unlike a bare
// last/count counter it inspects a bounded ring buffer of recent (call,result)
// observations, so it trips on genuine non-progress — the same call returning
// the same result, or a short oscillating cycle — and tolerates calls whose
// output keeps changing.
type loopDetector struct {
	window []observation
}

// wouldRepeat reports whether running a call with callHash would complete a run
// of identicalRunThreshold consecutive identical observations. It is consulted
// before a tool runs: when the previous K-1 observations are already identical
// to one another and share callHash, the result is overwhelmingly likely to
// repeat, so the guard trips without executing the futile call. This preserves
// the contract that the K-th identical invocation never runs.
func (d *loopDetector) wouldRepeat(callHash string) bool {
	need := identicalRunThreshold - 1
	if need <= 0 {
		return true
	}
	if len(d.window) < need {
		return false
	}
	tail := d.window[len(d.window)-need:]
	first := tail[0]
	if first.callHash != callHash {
		return false
	}
	for _, obs := range tail[1:] {
		if !obs.equal(first) {
			return false
		}
	}
	return true
}

// record appends an executed call's (call,result) observation to the ring
// buffer and reports whether the buffer now exhibits an A,B,A,B oscillation —
// two distinct observations alternating across the last four entries. That
// cycle is the other futile pattern: the agent flip-flops between two steps
// without converging. Identical-run detection is handled predictively by
// wouldRepeat before the call runs, so record covers only the cyclic case.
func (d *loopDetector) record(callHash, resultHash string, isError bool) bool {
	d.window = append(d.window, observation{
		callHash:   callHash,
		resultHash: resultHash,
		isError:    isError,
	})
	if len(d.window) > loopDetectorWindow {
		d.window = d.window[len(d.window)-loopDetectorWindow:]
	}
	return d.hasAlternatingCycle()
}

// hasAlternatingCycle reports whether the last four observations form an
// A,B,A,B pattern with A and B distinct.
func (d *loopDetector) hasAlternatingCycle() bool {
	n := len(d.window)
	if n < 4 {
		return false
	}
	a := d.window[n-4]
	b := d.window[n-3]
	if a.equal(b) {
		return false
	}
	return d.window[n-2].equal(a) && d.window[n-1].equal(b)
}

// resultHash canonicalises a tool result's content into a stable digest. The
// full content is hashed byte-for-byte: unlike the previous detector it does
// not strip trailing whitespace, so results that differ only in invisible
// characters are correctly treated as distinct. The error status is tracked
// separately on the observation, so it is not folded into this digest.
func resultHash(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:])
}

func toolCallHash(toolName string, args json.RawMessage) (string, error) {
	canonical, err := canonicalJSON(args)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256([]byte(toolName + "\x00" + canonical))
	return hex.EncodeToString(h[:]), nil
}

func canonicalJSON(raw json.RawMessage) (string, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage(`{}`)
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return "", fmt.Errorf("decoding tool arguments: %w", err)
	}
	buf := &bytes.Buffer{}
	writeCanonical(buf, value)
	return buf.String(), nil
}

func writeCanonical(buf *bytes.Buffer, value any) {
	switch v := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, key := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeCanonical(buf, key)
			buf.WriteByte(':')
			writeCanonical(buf, v[key])
		}
		buf.WriteByte('}')
	case []any:
		buf.WriteByte('[')
		for i, item := range v {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeCanonical(buf, item)
		}
		buf.WriteByte(']')
	case string:
		// Hash the string verbatim. Trailing whitespace is significant: stripping
		// it (as the previous detector did) could collapse genuinely distinct
		// arguments into a false loop signal.
		data, _ := json.Marshal(v)
		buf.Write(data)
	case json.Number:
		buf.WriteString(v.String())
	case bool:
		if v {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case nil:
		buf.WriteString("null")
	default:
		data, _ := json.Marshal(v)
		buf.Write(data)
	}
}
