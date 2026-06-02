// Package message defines the canonical conversation representation for BharatCode.
package message

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"
)

// encodeState carries a reusable byte buffer for assembling a Message's JSON.
// Reusing it through a pool is what keeps allocations low: the growable buffer
// is recycled across MarshalJSON calls instead of being allocated each time.
type encodeState struct {
	buf []byte
}

// encodeStatePool recycles encodeState buffers across MarshalJSON calls.
var encodeStatePool = sync.Pool{
	New: func() any { return &encodeState{buf: make([]byte, 0, 512)} },
}

// MarshalJSON serializes a Message into JSON.
//
// It produces byte-identical output to the equivalent reflection-based
// marshaler (a Content slice converted to []json.RawMessage and re-marshaled
// inside an envelope struct) while performing far fewer allocations. The
// envelope is assembled directly into a pooled buffer with hand-rolled string,
// integer, and time appenders that reproduce encoding/json's formatting and
// HTML escaping exactly. The byte-identity tests assert this against
// json.Marshal directly.
func (m Message) MarshalJSON() ([]byte, error) {
	es := encodeStatePool.Get().(*encodeState)
	es.buf = es.buf[:0]
	defer encodeStatePool.Put(es)

	b := es.buf
	b = append(b, '{')

	b = append(b, `"id":`...)
	b = appendEscapedString(b, m.ID)

	b = append(b, `,"session_id":`...)
	b = appendEscapedString(b, m.SessionID)

	b = append(b, `,"role":`...)
	b = appendEscapedString(b, string(m.Role))

	b = append(b, `,"content":`...)
	var err error
	b, err = appendContent(b, m.Content)
	if err != nil {
		es.buf = b
		return nil, err
	}

	// ParentID carries json:"parent_id,omitempty"; a nil pointer is omitted.
	if m.ParentID != nil {
		b = append(b, `,"parent_id":`...)
		b = appendEscapedString(b, *m.ParentID)
	}

	b = append(b, `,"created_at":`...)
	b, err = appendTime(b, m.CreatedAt)
	if err != nil {
		es.buf = b
		return nil, fmt.Errorf("marshalling created_at: %w", err)
	}

	// Usage carries json:"usage,omitempty"; a nil pointer is omitted.
	if m.Usage != nil {
		b = append(b, `,"usage":`...)
		b = appendUsage(b, m.Usage)
	}

	b = append(b, '}')

	es.buf = b

	// Copy out of the pooled buffer so the returned slice is independent.
	out := make([]byte, len(b))
	copy(out, b)
	return out, nil
}

// appendContent appends the JSON array of content blocks, emitting each block's
// "type" discriminator and fields directly so no intermediate
// []json.RawMessage is allocated.
func appendContent(b []byte, content []ContentBlock) ([]byte, error) {
	b = append(b, '[')
	for i, block := range content {
		if i > 0 {
			b = append(b, ',')
		}
		var err error
		b, err = appendBlock(b, i, block)
		if err != nil {
			return b, err
		}
	}
	b = append(b, ']')
	return b, nil
}

// appendBlock appends a single content block's JSON object. The field order and
// escaping mirror each block's own MarshalJSON: the "type" discriminator first,
// then the struct fields in declaration order. Unknown block types fall back to
// json.Marshal so custom ContentBlock implementations still serialize.
func appendBlock(b []byte, idx int, block ContentBlock) ([]byte, error) {
	switch v := block.(type) {
	case TextBlock:
		b = append(b, `{"type":"text","text":`...)
		b = appendEscapedString(b, v.Text)
		b = append(b, '}')

	case ToolUseBlock:
		b = append(b, `{"type":"tool_use","id":`...)
		b = appendEscapedString(b, v.ID)
		b = append(b, `,"name":`...)
		b = appendEscapedString(b, v.Name)
		b = append(b, `,"input":`...)
		var err error
		b, err = appendRawMessage(b, v.Input)
		if err != nil {
			return b, fmt.Errorf("marshalling content block at index %d: %w", idx, err)
		}
		b = append(b, '}')

	case ToolResultBlock:
		b = append(b, `{"type":"tool_result","tool_use_id":`...)
		b = appendEscapedString(b, v.ToolUseID)
		b = append(b, `,"content":`...)
		b = appendEscapedString(b, v.Content)
		b = append(b, `,"is_error":`...)
		if v.IsError {
			b = append(b, "true"...)
		} else {
			b = append(b, "false"...)
		}
		b = append(b, '}')

	case ImageBlock:
		b = append(b, `{"type":"image","mime_type":`...)
		b = appendEscapedString(b, v.MimeType)
		b = append(b, `,"data":`...)
		b = appendByteSlice(b, v.Data)
		b = append(b, '}')

	case ThinkingBlock:
		b = append(b, `{"type":"thinking","text":`...)
		b = appendEscapedString(b, v.Text)
		b = append(b, '}')

	default:
		// Unknown ContentBlock implementation: defer to encoding/json so the
		// block still serializes through its own MarshalJSON if it has one.
		raw, err := json.Marshal(block)
		if err != nil {
			return b, fmt.Errorf("marshalling content block at index %d: %w", idx, err)
		}
		b = append(b, raw...)
	}
	return b, nil
}

// appendUsage appends a *TokenUsage as JSON, reproducing the struct's tags
// (input_tokens and output_tokens always present; the cache fields omitempty).
// Callers must ensure u is non-nil.
func appendUsage(b []byte, u *TokenUsage) []byte {
	b = append(b, `{"input_tokens":`...)
	b = strconv.AppendInt(b, int64(u.InputTokens), 10)
	b = append(b, `,"output_tokens":`...)
	b = strconv.AppendInt(b, int64(u.OutputTokens), 10)
	if u.CacheReadTokens != 0 {
		b = append(b, `,"cache_read_tokens":`...)
		b = strconv.AppendInt(b, int64(u.CacheReadTokens), 10)
	}
	if u.CacheWriteTokens != 0 {
		b = append(b, `,"cache_write_tokens":`...)
		b = strconv.AppendInt(b, int64(u.CacheWriteTokens), 10)
	}
	b = append(b, '}')
	return b
}

// appendByteSlice appends a []byte as encoding/json would: a base64 (standard,
// padded) string, or null when the slice is nil.
func appendByteSlice(b, data []byte) []byte {
	if data == nil {
		return append(b, "null"...)
	}
	b = append(b, '"')
	b = base64.StdEncoding.AppendEncode(b, data)
	b = append(b, '"')
	return b
}

// appendRawMessage appends a json.RawMessage as encoding/json would: nil
// marshals to null, otherwise the value is compacted with HTML escaping
// applied. Deferring to json.Marshal here guarantees byte-identity for the
// arbitrary tool input payload, including whitespace compaction and the HTML
// escaping that json.Compact alone would not apply.
func appendRawMessage(b []byte, raw json.RawMessage) ([]byte, error) {
	if raw == nil {
		return append(b, "null"...), nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return b, err
	}
	return append(b, encoded...), nil
}

// appendTime appends a time.Time as encoding/json would via time.Time's own
// MarshalJSON: a quoted RFC 3339 string with sub-second precision. The common
// in-range case is hand-rolled with AppendFormat to avoid an allocation; the
// strict marshaler is used as a fallback for years outside [0,9999], which it
// reports as an error exactly as Time.MarshalJSON does.
func appendTime(b []byte, t time.Time) ([]byte, error) {
	if y := t.Year(); y < 0 || y > 9999 {
		raw, err := t.MarshalJSON()
		if err != nil {
			return b, err
		}
		return append(b, raw...), nil
	}
	b = append(b, '"')
	b = t.AppendFormat(b, time.RFC3339Nano)
	b = append(b, '"')
	return b, nil
}
