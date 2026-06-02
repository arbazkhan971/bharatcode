package agent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type loopDetector struct {
	last  string
	count int
}

func (d *loopDetector) observe(toolName string, args json.RawMessage) (bool, error) {
	hash, err := toolCallHash(toolName, args)
	if err != nil {
		return false, err
	}
	if hash == d.last {
		d.count++
	} else {
		d.last = hash
		d.count = 1
	}
	return d.count >= 3, nil
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
		data, _ := json.Marshal(strings.TrimRightFunc(v, func(r rune) bool {
			return r == ' ' || r == '\t' || r == '\r' || r == '\n'
		}))
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
