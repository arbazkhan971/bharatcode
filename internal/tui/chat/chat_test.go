package chat

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStreamRender_NoFlicker(t *testing.T) {
	t.Parallel()

	list := New()
	seen := make(map[string]struct{})
	for i := 0; i < 100; i++ {
		list.Stream("assistant-1", fmt.Sprintf("%02d ", i))
		seen[list.Render(80)] = struct{}{}
	}
	list.FinishStream("assistant-1")
	seen[list.Render(80)] = struct{}{}

	require.LessOrEqual(t, len(seen), 101)
	require.LessOrEqual(t, list.RenderRegions(), 101)
}
