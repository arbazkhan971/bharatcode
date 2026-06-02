package agent

import (
	"encoding/json"

	"github.com/arbazkhan971/bharatcode/internal/message"
)

const reservedResponseTokens = 4096

func truncateForContext(messages []message.Message, contextWindow int) []message.Message {
	if contextWindow <= 0 {
		return append([]message.Message(nil), messages...)
	}
	limit := contextWindow - reservedResponseTokens
	if limit < 1024 {
		limit = contextWindow
	}
	if len(messages) <= 2 {
		return append([]message.Message(nil), messages...)
	}

	latestUser := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == message.RoleUser {
			latestUser = i
			break
		}
	}

	outReverse := make([]message.Message, 0, len(messages))
	total := 0
	for i := len(messages) - 1; i >= 0; i-- {
		tokens := estimateMessageTokens(messages[i])
		keep := total+tokens <= limit || i == latestUser
		if keep {
			outReverse = append(outReverse, messages[i])
			total += tokens
		}
	}
	out := make([]message.Message, 0, len(outReverse))
	for i := len(outReverse) - 1; i >= 0; i-- {
		out = append(out, outReverse[i])
	}
	return out
}

func estimateMessageTokens(msg message.Message) int {
	data, err := json.Marshal(msg.Content)
	if err != nil {
		return 256
	}
	n := len(data) / 4
	if n < 1 {
		return 1
	}
	return n
}
