package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/session"
	"github.com/spf13/cobra"
)

// importedTranscript is the result of parsing an import source file into a
// shape that can be written to a new session.
type importedTranscript struct {
	// Title is the session title parsed from a "# Title" heading (markdown
	// transcripts) or empty so the repo auto-titles from the first message.
	Title string
	// Model is the model id parsed from a "- Model: ..." line, or empty.
	Model string
	// Agent is the agent name parsed from a "- Agent: ..." line, or empty.
	Agent string
	// Messages are the reconstructed turns, oldest first. Every block is a
	// text block, so importing never trips the message package's tool-pairing
	// invariants and the lossy markdown export round-trips faithfully for the
	// content it actually preserves.
	Messages []message.Message
}

func newImportHistoryCmd() *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "import-history <file>",
		Short: "Import a transcript or prompt list into a new session",
		Long: "Read a previously exported Markdown transcript, or a plain file with " +
			"one prompt per line, and store it as a new BharatCode session.\n\n" +
			"Markdown transcripts are split on their \"## Role\" headings; the prose " +
			"between headings becomes one text message per turn. Tool calls and tool " +
			"results in the export are folded into the surrounding turn's text, since " +
			"the Markdown form does not preserve the tool ids needed to rebuild " +
			"structured tool blocks. A prompts file becomes one user message per " +
			"non-empty line.",
		Args:    cobra.ExactArgs(1),
		Example: "  bharatcode import-history transcript.md\n  bharatcode import-history prompts.txt --format prompts",
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := os.ReadFile(args[0])
			if err != nil {
				return fmt.Errorf("reading import file %s: %w", args[0], err)
			}

			imported, err := parseImport(string(data), format)
			if err != nil {
				return err
			}
			if len(imported.Messages) == 0 {
				return fmt.Errorf("import file %s has no messages to import", args[0])
			}

			opts := getRootOptions(cmd)
			application, err := buildApp(cmd.Context(), opts)
			if err != nil {
				return err
			}
			defer closeApp(cmd.Context(), application)

			projectPath := opts.projectDir
			if projectPath == "" {
				if cwd, err := os.Getwd(); err == nil {
					projectPath = cwd
				}
			}

			sess := &session.Session{
				ProjectPath: projectPath,
				Title:       imported.Title,
				Model:       imported.Model,
				Agent:       imported.Agent,
			}
			if err := application.Sessions.Create(cmd.Context(), sess); err != nil {
				return fmt.Errorf("creating session: %w", err)
			}

			for _, msg := range imported.Messages {
				if err := application.Sessions.AppendMessage(cmd.Context(), sess.ID, msg); err != nil {
					return fmt.Errorf("importing message: %w", err)
				}
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(),
				"Imported %d messages into session %s\n", len(imported.Messages), sess.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "markdown",
		"source format: \"markdown\" (an exported transcript) or \"prompts\" (one prompt per line)")
	return cmd
}

// parseImport dispatches to the parser for the requested format.
func parseImport(data, format string) (importedTranscript, error) {
	switch format {
	case "", "markdown", "md":
		return parseMarkdownTranscript(data), nil
	case "prompts", "prompt", "lines":
		return parsePrompts(data), nil
	default:
		return importedTranscript{}, fmt.Errorf("invalid --format %q (want \"markdown\" or \"prompts\")", format)
	}
}

// parsePrompts builds one user text message per non-empty, non-comment line.
// Lines that are blank after trimming are skipped so empty content never
// reaches the store; lines beginning with "#" are treated as comments. The
// session is left untitled so the repo auto-titles from the first prompt.
func parsePrompts(data string) importedTranscript {
	var msgs []message.Message
	base := time.Now().UTC()
	for _, line := range strings.Split(data, "\n") {
		text := strings.TrimSpace(strings.TrimRight(line, "\r"))
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		msgs = append(msgs, message.Message{
			Role:      message.RoleUser,
			Content:   []message.ContentBlock{message.TextBlock{Text: text}},
			CreatedAt: base.Add(time.Duration(len(msgs)) * time.Second),
		})
	}
	return importedTranscript{Messages: msgs}
}

// roleForLabel maps a transcript "## Role" heading back to a message role,
// inverting roleLabel from the export side. Unknown labels default to user so
// arbitrary transcripts still import as readable text.
func roleForLabel(label string) message.Role {
	switch strings.ToLower(strings.TrimSpace(label)) {
	case "assistant":
		return message.RoleAssistant
	case "system":
		return message.RoleSystem
	case "tool":
		return message.RoleTool
	default:
		return message.RoleUser
	}
}

// isItalicLine reports whether s is a single Markdown italic run like
// "*2026-06-02 10:30:00 UTC*", which the export uses for the per-turn
// timestamp. It requires the asterisks to wrap a non-empty body so a stray
// "*" or "**bold**" marker is not mistaken for the timestamp line.
func isItalicLine(s string) bool {
	if len(s) < 3 || s[0] != '*' || s[len(s)-1] != '*' {
		return false
	}
	inner := s[1 : len(s)-1]
	// "**...**" (bold) starts/ends with another asterisk; the timestamp line
	// never does, so reject it to avoid eating real bold content.
	return inner != "" && inner[0] != '*' && inner[len(inner)-1] != '*'
}

// parseMarkdownTranscript reconstructs role-attributed text messages from a
// Markdown transcript produced by session.ExportMarkdown. It reads the leading
// "# Title", "- Model:" and "- Agent:" header lines, then splits the body on
// "## Role" headings. The prose of each turn (including any fenced tool blocks,
// kept verbatim as text) becomes a single text message. Turns whose text is
// empty after trimming are dropped so no message reaches the store without
// content.
func parseMarkdownTranscript(data string) importedTranscript {
	var result importedTranscript

	var (
		curRole     message.Role
		curLines    []string
		haveTurn    bool
		sawTurnText bool
		inHeader    = true // Before the first "## Role" heading we are in the document header.
	)

	flush := func() {
		if !haveTurn {
			return
		}
		text := strings.TrimSpace(strings.Join(curLines, "\n"))
		if text != "" {
			result.Messages = append(result.Messages, message.Message{
				Role:      curRole,
				Content:   []message.ContentBlock{message.TextBlock{Text: text}},
				CreatedAt: time.Now().UTC().Add(time.Duration(len(result.Messages)) * time.Second),
			})
		}
		curLines = curLines[:0]
		haveTurn = false
	}

	for _, raw := range strings.Split(data, "\n") {
		line := strings.TrimRight(raw, "\r")

		if label, ok := strings.CutPrefix(line, "## "); ok {
			flush()
			curRole = roleForLabel(label)
			haveTurn = true
			sawTurnText = false
			inHeader = false
			continue
		}

		if inHeader {
			if title, ok := strings.CutPrefix(line, "# "); ok {
				result.Title = strings.TrimSpace(title)
				continue
			}
			if model, ok := strings.CutPrefix(line, "- Model:"); ok {
				result.Model = strings.TrimSpace(model)
				continue
			}
			if agent, ok := strings.CutPrefix(line, "- Agent:"); ok {
				result.Agent = strings.TrimSpace(agent)
				continue
			}
			continue
		}

		// Inside a turn the export emits, right after the heading and a blank
		// line, an italic "*timestamp*" line. Drop that one metadata line (and
		// any blank lines preceding it) so it does not pollute the message text;
		// strings.TrimSpace on flush removes the remaining leading blank.
		trimmed := strings.TrimSpace(line)
		if !sawTurnText {
			if trimmed == "" {
				continue
			}
			if isItalicLine(trimmed) {
				sawTurnText = true // Consume exactly this one timestamp line.
				continue
			}
			sawTurnText = true
		}
		curLines = append(curLines, line)
	}
	flush()

	return result
}
