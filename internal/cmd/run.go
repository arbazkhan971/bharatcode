package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/agent"
	"github.com/arbazkhan971/bharatcode/internal/app"
	"github.com/arbazkhan971/bharatcode/internal/filetracker"
	"github.com/arbazkhan971/bharatcode/internal/identity"
	"github.com/arbazkhan971/bharatcode/internal/ledger"
	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/session"
	"github.com/spf13/cobra"
)

func newRunCmd() *cobra.Command {
	var modelName string
	var agentName string
	var jsonStream bool
	var outputLastMessage string
	var quiet bool
	var continueSession bool
	var resumeSessionID string
	cmd := &cobra.Command{
		Use:   "run [prompt]",
		Short: "Run one prompt without opening the TUI",
		Example: "  bharatcode run \"summarize this repository\"\n" +
			"  echo \"hello\" | bharatcode run\n" +
			"  bharatcode run --continue \"what's next?\"\n" +
			"  bharatcode run --session <id> \"follow up question\"",
		RunE: func(cmd *cobra.Command, args []string) error {
			prompt, err := readPrompt(cmd, args)
			if err != nil {
				return err
			}
			if answer, ok := identity.Answer(prompt); ok {
				if jsonStream {
					return emitLocalIdentityJSON(cmd.OutOrStdout(), answer)
				}
				if outputLastMessage != "" {
					if err := os.WriteFile(outputLastMessage, []byte(answer), 0o644); err != nil {
						return fmt.Errorf("writing last message: %w", err)
					}
				}
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), answer)
				return nil
			}
			opts := getRootOptions(cmd)
			application, err := buildApp(cmd.Context(), opts)
			if err != nil {
				return err
			}
			defer closeApp(cmd.Context(), application)

			projectPath := opts.projectDir
			if projectPath == "" {
				projectPath = "."
			}

			s, err := resolveRunSession(cmd.Context(), application, projectPath,
				resumeSessionID, modelName, agentName, prompt, continueSession)
			if err != nil {
				return err
			}

			// Prefer an explicit --agent flag; fall back to the session's stored
			// agent (useful when --continue or --session resumes a prior run);
			// ultimately default to "coder".
			effectiveAgent := agentName
			if effectiveAgent == "" && s.Agent != "" {
				effectiveAgent = s.Agent
			}
			if effectiveAgent == "" {
				effectiveAgent = "coder"
			}
			loop, err := application.Agent.Agent(effectiveAgent)
			if err != nil {
				return fmt.Errorf("resolving agent: %w", err)
			}

			// A --model override must re-point the loop at the provider that owns
			// that model, not just change the model id in the request: the loop is
			// bound to its agent's default provider at construction, so without
			// this a "--model gpt-5.1-codex" would still stream to the default
			// (e.g. deepseek) provider and fail auth. SetActiveModel resolves the
			// owning provider; SetModel rebinds the live loop atomically.
			if modelName != "" {
				provider, err := application.Agent.SetActiveModel(effectiveAgent, modelName)
				if err != nil {
					return fmt.Errorf("selecting model %q: %w", modelName, err)
				}
				loop.SetModel(modelName, provider)
			}

			var before workspaceSnapshot
			if !jsonStream {
				before = snapshotWorkspace(projectPath)
			}

			if jsonStream {
				if err := runJSON(cmd, application, loop, s.ID, prompt); err != nil {
					return err
				}
			} else if err := loop.Run(cmd.Context(), s.ID, userMessage(prompt)); err != nil {
				return fmt.Errorf("running prompt: %w", err)
			}

			messages, err := application.Sessions.Messages(cmd.Context(), s.ID)
			if err != nil {
				return fmt.Errorf("loading response: %w", err)
			}
			response := finalRunOutput(messages)

			if outputLastMessage != "" {
				if err := os.WriteFile(outputLastMessage, []byte(response), 0o644); err != nil {
					return fmt.Errorf("writing last message: %w", err)
				}
			}
			if !jsonStream {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), response)
				printChangedFiles(cmd.Context(), cmd.OutOrStdout(), application.FileTracker, s.ID, diffWorkspace(projectPath, before))
			}

			if !quiet {
				printRunSummary(cmd.Context(), cmd.ErrOrStderr(), application.Ledger, s.ID)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&modelName, "model", "", "model id to use")
	cmd.Flags().StringVar(&agentName, "agent", "", "agent name to use (default: coder, or the resumed session's agent)")
	cmd.Flags().BoolVar(&jsonStream, "json", false, "stream agent events as newline-delimited JSON")
	cmd.Flags().StringVar(&outputLastMessage, "output-last-message", "", "write the final assistant message to this file")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "suppress the token/cost summary printed to stderr after each run")
	cmd.Flags().BoolVarP(&continueSession, "continue", "c", false, "continue the most recent session for this project")
	cmd.Flags().StringVar(&resumeSessionID, "session", "", "continue a specific session by ID")
	cmd.MarkFlagsMutuallyExclusive("continue", "session")
	return cmd
}

// resolveRunSession returns the session for the run subcommand. When sessionID
// is non-empty the named session is loaded (error if absent). When
// continueRecent is true the most recent session for projectPath is reused; if
// none exists a fresh session is created instead. Otherwise a new session is
// always created.
func resolveRunSession(ctx context.Context, application *app.App, projectPath,
	sessionID, modelName, agentName, prompt string, continueRecent bool) (*session.Session, error) {

	if sessionID != "" {
		s, err := application.Sessions.Get(ctx, sessionID)
		if err != nil {
			return nil, fmt.Errorf("loading session %q: %w", sessionID, err)
		}
		return s, nil
	}

	if continueRecent {
		sessions, err := application.Sessions.List(ctx, session.ListFilter{
			ProjectPath: projectPath,
			Limit:       1,
		})
		if err == nil && len(sessions) > 0 {
			return &sessions[0], nil
		}
		// No prior session: fall through and create a new one.
	}

	s := &session.Session{
		ProjectPath: projectPath,
		Title:       session.TitleFromFirstMessage(userMessage(prompt)),
		Model:       modelName,
		Agent:       agentName,
	}
	if err := application.Sessions.Create(ctx, s); err != nil {
		return nil, fmt.Errorf("creating session: %w", err)
	}
	return s, nil
}

// printRunSummary queries the ledger for the session's token and cost totals and
// writes a one-line summary to stderr. It is a no-op when l is nil (no ledger
// configured) or when the session recorded no calls (e.g. a dry-run or an error
// before the first provider turn).
func printRunSummary(ctx context.Context, w io.Writer, l *ledger.Ledger, sessionID string) {
	if l == nil {
		return
	}
	sum, err := l.Summary(ctx, sessionID, ledger.WindowSession)
	if err != nil || sum.CallCount == 0 {
		return
	}
	_, _ = fmt.Fprintln(w, formatRunSummary(sum))
}

// formatRunSummary formats a ledger.Summary as a compact one-line token/cost
// string suitable for printing after a non-interactive run. Cost is appended
// only when non-zero (local/free models carry no pricing).
func formatRunSummary(sum ledger.Summary) string {
	s := fmt.Sprintf("Tokens: %d in, %d out", sum.InputTokens, sum.OutputTokens)
	if sum.CostINR > 0 {
		s += fmt.Sprintf(" · Cost: ₹%s", formatRupees(sum.CostINR))
	}
	return s
}

// printChangedFiles prints a short, deduplicated absolute-path summary of the
// files the run touched, each tagged with an operation label
// (created/modified/deleted). It keeps the final CLI output useful for
// file-creation tasks without forcing the user to dig through the transcript.
type fileChange struct {
	path  string
	label string
}

func printChangedFiles(ctx context.Context, w io.Writer, tracker *filetracker.Tracker, sessionID string, fallback []fileChange) {
	labels := map[string]string{}
	if tracker != nil && sessionID != "" {
		changes, err := tracker.ChangesForSession(ctx, sessionID)
		if err == nil {
			for _, ch := range changes {
				if ch.Path == "" {
					continue
				}
				labels[ch.Path] = mergeChangeLabel(labels[ch.Path], ch.Op)
			}
		}
	}
	for _, ch := range fallback {
		if ch.path == "" {
			continue
		}
		if _, exists := labels[ch.path]; !exists {
			labels[ch.path] = ch.label
		}
	}
	if len(labels) == 0 {
		return
	}

	paths := make([]string, 0, len(labels))
	for path := range labels {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	printChangedFileList(w, paths, labels)
}

func printChangedFileList(w io.Writer, paths []string, labels map[string]string) {
	if len(paths) == 0 {
		return
	}

	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "Changed files:")
	for _, path := range paths {
		if label := labels[path]; label != "" {
			_, _ = fmt.Fprintf(w, "- %s (%s)\n", path, label)
		} else {
			_, _ = fmt.Fprintf(w, "- %s\n", path)
		}
	}
}

type workspaceFile struct {
	size    int64
	modTime time.Time
}

type workspaceSnapshot map[string]workspaceFile

func snapshotWorkspace(root string) workspaceSnapshot {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil
	}
	snap := workspaceSnapshot{}
	_ = filepath.WalkDir(absRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", ".bharatcode":
				if path != absRoot {
					return filepath.SkipDir
				}
			}
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		snap[path] = workspaceFile{size: info.Size(), modTime: info.ModTime()}
		return nil
	})
	return snap
}

func diffWorkspace(root string, before workspaceSnapshot) []fileChange {
	if before == nil {
		return nil
	}
	after := snapshotWorkspace(root)
	if after == nil {
		return nil
	}
	var changes []fileChange
	for path, next := range after {
		prev, existed := before[path]
		switch {
		case !existed:
			changes = append(changes, fileChange{path: path, label: "created"})
		case prev.size != next.size || !prev.modTime.Equal(next.modTime):
			changes = append(changes, fileChange{path: path, label: "modified"})
		}
	}
	for path := range before {
		if _, exists := after[path]; !exists {
			changes = append(changes, fileChange{path: path, label: "deleted"})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].path < changes[j].path })
	return changes
}

// mergeChangeLabel folds a new operation into the running label for a path.
// prev is "" on the first change for that path. A delete always wins (it is
// the file's net state), a create is preserved through later edits, and an
// edit only sets the label when the file was not already created in this run.
func mergeChangeLabel(prev string, op filetracker.Operation) string {
	switch op {
	case filetracker.OpCreate:
		return "created"
	case filetracker.OpDelete:
		return "deleted"
	case filetracker.OpEdit:
		if prev == "created" {
			return prev
		}
		return "modified"
	default:
		if prev != "" {
			return prev
		}
		return string(op)
	}
}

// runJSON drives loop while streaming each agent.Event to stdout as one JSON
// object per line, flushing after every line. It subscribes to the agent bus
// before the run starts so no event is missed, and drains all buffered events
// before returning.
func runJSON(cmd *cobra.Command, application *app.App, loop *agent.Loop, sessionID, prompt string) error {
	events, cancel := application.Bus.Agent.Subscribe()
	defer cancel()

	out := cmd.OutOrStdout()
	enc := json.NewEncoder(out)

	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- loop.Run(cmd.Context(), sessionID, userMessage(prompt))
	}()

	var runErr error
	done := false
	for !done {
		select {
		case ev := <-events:
			emitEvent(enc, out, ev)
		case runErr = <-runErrCh:
			done = true
		}
	}

	// Drain events the loop published before returning. They are already in the
	// buffered subscriber channel, so this never blocks.
	for {
		select {
		case ev := <-events:
			emitEvent(enc, out, ev)
		default:
			if runErr != nil {
				return fmt.Errorf("running prompt: %w", runErr)
			}
			return nil
		}
	}
}

// emitEvent encodes ev as one NDJSON line and flushes it immediately.
func emitEvent(enc *json.Encoder, out io.Writer, ev agent.Event) {
	// json.Encoder.Encode appends a trailing newline, giving NDJSON framing.
	_ = enc.Encode(newRunEvent(ev))
	if flusher, ok := out.(interface{ Flush() error }); ok {
		_ = flusher.Flush()
	} else if syncer, ok := out.(interface{ Sync() error }); ok {
		_ = syncer.Sync()
	}
}

func userMessage(text string) message.Message {
	return message.Message{
		Role:      message.RoleUser,
		Content:   []message.ContentBlock{message.TextBlock{Text: text}},
		CreatedAt: time.Now().UTC(),
	}
}

func lastAssistantText(messages []message.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != message.RoleAssistant {
			continue
		}
		var parts []string
		for _, block := range messages[i].Content {
			if text, ok := block.(message.TextBlock); ok {
				parts = append(parts, text.Text)
			}
			if text, ok := block.(*message.TextBlock); ok {
				parts = append(parts, text.Text)
			}
		}
		return strings.Join(parts, "")
	}
	return ""
}

// finalRunOutput prefers the last assistant text, but falls back to the last
// tool result when a turn ends after a simple file-writing tool. That keeps the
// headless completion output useful even when the model does not add a prose
// closing line of its own.
func finalRunOutput(messages []message.Message) string {
	if text := strings.TrimSpace(lastAssistantText(messages)); text != "" {
		return text
	}
	return lastToolResultText(messages)
}

// lastToolResultText returns the raw content of the most recent tool result in
// the transcript, or "" when none exists.
func lastToolResultText(messages []message.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		for _, block := range messages[i].Content {
			if result, ok := block.(message.ToolResultBlock); ok && result.Content != "" {
				return result.Content
			}
			if result, ok := block.(*message.ToolResultBlock); ok && result.Content != "" {
				return result.Content
			}
		}
	}
	return ""
}
