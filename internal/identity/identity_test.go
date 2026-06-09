package identity

import (
	"strings"
	"testing"
)

func TestAnswerHandlesSimpleIdentityQuestions(t *testing.T) {
	cases := []string{
		"who are you?",
		"What are you",
		"what is BharatCode?",
		"what is bharat code",
		"tell me about yourself",
		"are you ChatGPT?",
		"identify yourself",
	}
	for _, input := range cases {
		t.Run(input, func(t *testing.T) {
			answer, ok := Answer(input)
			if !ok {
				t.Fatalf("Answer(%q) did not match", input)
			}
			if !strings.Contains(answer, "BharatCode") {
				t.Fatalf("answer missing BharatCode: %q", answer)
			}
			for _, forbidden := range []string{"I am ChatGPT", "I am OpenAI", "I am Codex", "I am Claude"} {
				if strings.Contains(answer, forbidden) {
					t.Fatalf("answer contains forbidden identity %q: %q", forbidden, answer)
				}
			}
		})
	}
}

func TestAnswerLetsRepoContextQuestionsReachAgent(t *testing.T) {
	cases := []string{
		"who are you in this repo?",
		"what model are you using?",
		"what is BharatCode version here?",
		"tell me about BharatCode configuration",
	}
	for _, input := range cases {
		t.Run(input, func(t *testing.T) {
			if answer, ok := Answer(input); ok {
				t.Fatalf("Answer(%q) = %q, want no deterministic answer", input, answer)
			}
		})
	}
}
