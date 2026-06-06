package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestThinkTool_Name(t *testing.T) {
	assert.Equal(t, "think", newThinkTool(Dependencies{}).Name())
}

func TestThinkTool_SchemaValid(t *testing.T) {
	var schema map[string]any
	require.NoError(t, json.Unmarshal(newThinkTool(Dependencies{}).Schema(), &schema))
	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, props, "thought")
	// thought must be required
	required, _ := schema["required"].([]any)
	var found bool
	for _, r := range required {
		if r == "thought" {
			found = true
		}
	}
	assert.True(t, found, "thought must be in required")
}

func TestThinkTool_BasicRoundtrip(t *testing.T) {
	tool := newThinkTool(Dependencies{})
	thought := "I need to check foo.go before editing bar.go to avoid import cycles."
	raw, _ := json.Marshal(map[string]string{"thought": thought})
	res, err := tool.Run(context.Background(), raw)
	require.NoError(t, err)
	assert.False(t, res.IsError)
	assert.Equal(t, thought, res.Content)
	assert.False(t, res.VerifyNeeded)
}

func TestThinkTool_NoSideEffects(t *testing.T) {
	// Result must not set VerifyNeeded or StopTurn.
	tool := newThinkTool(Dependencies{})
	raw, _ := json.Marshal(map[string]string{"thought": "some reasoning"})
	res, err := tool.Run(context.Background(), raw)
	require.NoError(t, err)
	assert.False(t, res.VerifyNeeded)
	assert.False(t, res.StopTurn)
	assert.False(t, res.IsError)
}

func TestThinkTool_EmptyThought(t *testing.T) {
	tool := newThinkTool(Dependencies{})
	raw, _ := json.Marshal(map[string]string{"thought": ""})
	res, err := tool.Run(context.Background(), raw)
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

func TestThinkTool_MissingThought(t *testing.T) {
	tool := newThinkTool(Dependencies{})
	res, err := tool.Run(context.Background(), json.RawMessage(`{}`))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

func TestThinkTool_MultilineThought(t *testing.T) {
	tool := newThinkTool(Dependencies{})
	thought := "Step 1: read the file.\nStep 2: find the bug.\nStep 3: write the fix."
	raw, _ := json.Marshal(map[string]string{"thought": thought})
	res, err := tool.Run(context.Background(), raw)
	require.NoError(t, err)
	assert.False(t, res.IsError)
	assert.Equal(t, thought, res.Content)
}
