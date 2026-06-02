package message

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// legacyMarshalMessage reproduces the previous reflection-based Message
// marshaler: each block is marshaled to json.RawMessage and re-marshaled
// inside an envelope struct. It is the byte-for-byte reference the optimized
// Message.MarshalJSON must match. It is defined only in the test so the
// optimized path is checked against a frozen baseline rather than itself.
func legacyMarshalMessage(m Message) ([]byte, error) {
	rawBlocks := make([]json.RawMessage, 0, len(m.Content))
	for _, block := range m.Content {
		raw, err := json.Marshal(block)
		if err != nil {
			return nil, err
		}
		rawBlocks = append(rawBlocks, raw)
	}

	aux := struct {
		ID        string            `json:"id"`
		SessionID string            `json:"session_id"`
		Role      Role              `json:"role"`
		Content   []json.RawMessage `json:"content"`
		ParentID  *string           `json:"parent_id,omitempty"`
		CreatedAt time.Time         `json:"created_at"`
		Usage     *TokenUsage       `json:"usage,omitempty"`
	}{
		ID:        m.ID,
		SessionID: m.SessionID,
		Role:      m.Role,
		Content:   rawBlocks,
		ParentID:  m.ParentID,
		CreatedAt: m.CreatedAt,
		Usage:     m.Usage,
	}

	return json.Marshal(aux)
}

// allControlBytes returns a string containing every ASCII control byte
// 0x00-0x1F, exercising the \u00XX escape path including 0x08 and 0x0C which
// have no short form in encoding/json's HTML-escaped output.
func allControlBytes() string {
	var sb strings.Builder
	for c := byte(0); c < 0x20; c++ {
		sb.WriteByte(c)
	}
	return sb.String()
}

// allBytes returns a string containing every byte value 0x00-0xFF, exercising
// DEL (0x7F), the HTML-sensitive bytes, and the high bytes that decode to
// U+FFFD, all in a single golden comparison against json.Marshal.
func allBytes() string {
	b := make([]byte, 256)
	for i := range b {
		b[i] = byte(i)
	}
	return string(b)
}

// representativeMessage exercises every block type, the omitempty fields
// (parent_id and usage), HTML-sensitive characters, and a json.RawMessage that
// contains insignificant whitespace so the byte-identity check covers the
// escaping and compaction behavior the optimized marshaler must reproduce.
func representativeMessage() Message {
	parent := "parent-<id>"
	return Message{
		ID:        "msg-1<&>",
		SessionID: "sess-1",
		Role:      RoleAssistant,
		ParentID:  &parent,
		CreatedAt: time.Date(2026, 6, 2, 10, 30, 15, 0, time.UTC),
		Usage:     &TokenUsage{InputTokens: 12, OutputTokens: 8, CacheReadTokens: 3},
		Content: []ContentBlock{
			TextBlock{Text: "Hello <world> & \"friends\"\n"},
			ThinkingBlock{Text: "Let me think..."},
			ToolUseBlock{
				ID:    "t-1",
				Name:  "bash",
				Input: json.RawMessage(`{ "command" : "ls <dir> & pwd" ,  "n": 2 }`),
			},
			ImageBlock{MimeType: "image/png", Data: []byte{0xde, 0xad, 0xbe, 0xef}},
			AttachmentBlock{
				Filename: "report <final>.pdf",
				MimeType: "application/pdf",
				Data:     []byte{0x25, 0x50, 0x44, 0x46},
				Path:     "/tmp/report <final>.pdf",
				Size:     4,
			},
			ToolResultBlock{ToolUseID: "t-1", Content: "out<put>", IsError: true},
		},
	}
}

func TestMarshalJSON_ByteIdenticalToLegacy(t *testing.T) {
	msg := representativeMessage()

	want, err := legacyMarshalMessage(msg)
	require.NoError(t, err)

	got, err := json.Marshal(msg)
	require.NoError(t, err)

	require.Equal(t, string(want), string(got), "optimized marshal output must be byte-identical to the legacy marshaler")
}

// TestMarshalJSON_ByteIdenticalAcrossCases checks byte-identity across edge
// cases that drive divergence between hand-rolled and reflection-based
// encoding: empty content, nil omitempty fields, false bools, nil tool_use
// input, nil image data, control bytes, invalid UTF-8, the U+2028/U+2029
// separators, fractional/trailing-zero/offset times, and the usage cache
// fields.
func TestMarshalJSON_ByteIdenticalAcrossCases(t *testing.T) {
	cases := map[string]Message{
		"empty_content_no_optionals": {
			ID:        "m",
			SessionID: "s",
			Role:      RoleUser,
			CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			Content:   nil,
		},
		"false_bool_and_nil_input": {
			ID:        "m",
			SessionID: "s",
			Role:      RoleAssistant,
			CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			Content: []ContentBlock{
				ToolUseBlock{ID: "t", Name: "n"}, // nil Input
				ToolResultBlock{ToolUseID: "t", Content: "", IsError: false},
			},
		},
		"nil_image_data": {
			ID:        "m",
			SessionID: "s",
			Role:      RoleUser,
			CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			Content: []ContentBlock{
				ImageBlock{MimeType: "image/jpeg", Data: nil},
			},
		},
		"empty_image_data": {
			ID:        "m",
			SessionID: "s",
			Role:      RoleUser,
			CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			Content: []ContentBlock{
				ImageBlock{MimeType: "image/jpeg", Data: []byte{}},
			},
		},
		"unicode_separators_and_emoji": {
			ID:        "m",
			SessionID: "s",
			Role:      RoleUser,
			CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			Content: []ContentBlock{
				// U+2028 LINE SEPARATOR and U+2029 PARAGRAPH SEPARATOR are
				// escaped unconditionally by encoding/json.
				TextBlock{Text: "tab\tnl\nemoji\U0001F600 ls  ps  end"},
			},
		},
		"all_control_bytes": {
			ID:        "m",
			SessionID: "s",
			Role:      RoleUser,
			CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			Content: []ContentBlock{
				TextBlock{Text: allControlBytes()},
			},
		},
		"all_256_bytes": {
			ID:        "m",
			SessionID: "s",
			Role:      RoleUser,
			CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			Content: []ContentBlock{
				// Every byte 0x00-0xFF in one string: all control bytes, DEL
				// (0x7F), the HTML-sensitive bytes, and the high bytes that
				// decode to U+FFFD. This closes every escaping hole at once.
				TextBlock{Text: allBytes()},
			},
		},
		"invalid_utf8": {
			ID:        "m",
			SessionID: "s",
			Role:      RoleUser,
			CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			Content: []ContentBlock{
				// Lone continuation/start bytes that decode to U+FFFD.
				TextBlock{Text: "valid\xff\xfe\x80tail"},
			},
		},
		"time_with_fractional_seconds": {
			ID:        "m",
			SessionID: "s",
			Role:      RoleUser,
			CreatedAt: time.Date(2026, 6, 2, 10, 30, 15, 123456789, time.UTC),
			Content:   []ContentBlock{TextBlock{Text: "x"}},
		},
		"time_with_trailing_zero_nanos": {
			ID:        "m",
			SessionID: "s",
			Role:      RoleUser,
			CreatedAt: time.Date(2026, 6, 2, 10, 30, 15, 500000000, time.UTC),
			Content:   []ContentBlock{TextBlock{Text: "x"}},
		},
		"time_with_offset_zone": {
			ID:        "m",
			SessionID: "s",
			Role:      RoleUser,
			CreatedAt: time.Date(2026, 6, 2, 10, 30, 15, 0, time.FixedZone("IST", 5*3600+1800)),
			Content:   []ContentBlock{TextBlock{Text: "x"}},
		},
		"all_usage_fields": {
			ID:        "m",
			SessionID: "s",
			Role:      RoleAssistant,
			CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			Usage:     &TokenUsage{InputTokens: 100, OutputTokens: 50, CacheReadTokens: 25, CacheWriteTokens: 10},
			Content:   []ContentBlock{TextBlock{Text: "x"}},
		},
		"zero_usage_fields": {
			ID:        "m",
			SessionID: "s",
			Role:      RoleAssistant,
			CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			Usage:     &TokenUsage{InputTokens: 0, OutputTokens: 0},
			Content:   []ContentBlock{TextBlock{Text: "x"}},
		},
		"raw_input_with_html_and_whitespace": {
			ID:        "m",
			SessionID: "s",
			Role:      RoleAssistant,
			CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			Content: []ContentBlock{
				ToolUseBlock{
					ID:    "t",
					Name:  "n",
					Input: json.RawMessage("{ \"q\": \"a<b>&c\" ,\n  \"nested\": [1,  2] }"),
				},
			},
		},
		"with_parent_id": {
			ID:        "m",
			SessionID: "s",
			Role:      RoleUser,
			ParentID:  ptr("p<1>"),
			CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			Content:   []ContentBlock{TextBlock{Text: "x"}},
		},
	}

	for name, msg := range cases {
		t.Run(name, func(t *testing.T) {
			want, err := legacyMarshalMessage(msg)
			require.NoError(t, err)

			got, err := json.Marshal(msg)
			require.NoError(t, err)

			require.Equal(t, string(want), string(got))
		})
	}
}

func ptr[T any](v T) *T { return &v }

// TestMarshalJSON_RoundTripStable ensures the optimized output still decodes
// back to an equivalent Message and re-marshals to identical bytes.
func TestMarshalJSON_RoundTripStable(t *testing.T) {
	msg := representativeMessage()

	data, err := json.Marshal(msg)
	require.NoError(t, err)

	var decoded Message
	require.NoError(t, json.Unmarshal(data, &decoded))

	require.Equal(t, msg.ID, decoded.ID)
	require.Equal(t, msg.SessionID, decoded.SessionID)
	require.Equal(t, msg.Role, decoded.Role)
	require.Equal(t, *msg.ParentID, *decoded.ParentID)
	require.True(t, decoded.CreatedAt.Equal(msg.CreatedAt))
	require.Equal(t, *msg.Usage, *decoded.Usage)
	require.Len(t, decoded.Content, len(msg.Content))

	redata, err := json.Marshal(decoded)
	require.NoError(t, err)
	require.Equal(t, string(data), string(redata))
}

// BenchmarkMarshalMessage_Optimized measures the end-to-end json.Marshal(msg)
// path on the representative message. This carries unavoidable wrapper
// overhead (boxing msg into any and copying the returned slice), so it floors
// above the marshaler's own allocation count.
func BenchmarkMarshalMessage_Optimized(b *testing.B) {
	msg := representativeMessage()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := json.Marshal(msg); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkMarshalMessageDirect_TextOnly isolates Message.MarshalJSON on a
// clean text-only message (no tool_use, no usage), which is the shape the
// allocation target was audited against. It excludes the json.Marshal wrapper
// overhead so the marshaler's own allocation count is visible.
func BenchmarkMarshalMessageDirect_TextOnly(b *testing.B) {
	text := strings.Repeat("a", 1024)
	msg := Message{
		ID:        "msg-bench",
		SessionID: "sess-bench",
		Role:      RoleUser,
		Content:   []ContentBlock{TextBlock{Text: text}},
		CreatedAt: time.Now(),
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := msg.MarshalJSON(); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkMarshalMessageDirect_Mixed isolates Message.MarshalJSON on the
// representative mixed-block message.
func BenchmarkMarshalMessageDirect_Mixed(b *testing.B) {
	msg := representativeMessage()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := msg.MarshalJSON(); err != nil {
			b.Fatal(err)
		}
	}
}
