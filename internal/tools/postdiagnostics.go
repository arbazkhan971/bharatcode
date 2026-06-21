package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/lsp"
)

// editDiagnoser re-checks a file after it is written and reports the problems a
// language server found. *lsp.Manager satisfies it; tests supply a fake. It is
// the surface the edit/write/multiedit tools use to show the model errors it
// just introduced, matching opencode/Claude-Code behaviour.
type editDiagnoser interface {
	NotifyChange(ctx context.Context, path string) error
	Diagnostics(ctx context.Context, path string) ([]lsp.Diagnostic, error)
}

// postWriteDiagnosticsTimeout bounds how long the post-edit diagnostics probe
// waits for the language server. The probe is best-effort feedback layered on
// top of a successful write, so it must never stall the edit: if the server is
// slow or silent the note is simply omitted.
const postWriteDiagnosticsTimeout = 3 * time.Second

// maxPostWriteDiagnostics caps how many problems are appended to a write result
// so a file with hundreds of errors cannot flood the tool output.
const maxPostWriteDiagnostics = 20

// postWriteDiagnostics syncs the just-written file with its language server and
// returns a human-readable note describing the errors and warnings found, or
// the empty string when there is no server, no problem, or the server did not
// respond in time. Info/hint-level diagnostics are intentionally dropped to
// keep the signal actionable. It never returns an error: diagnostics are
// advisory and a failure here must not fail the edit.
func postWriteDiagnostics(ctx context.Context, src editDiagnoser, workDir, path string) string {
	if src == nil {
		return ""
	}

	probeCtx, cancel := context.WithTimeout(ctx, postWriteDiagnosticsTimeout)
	defer cancel()

	// Tell the server the file changed so its analysis reflects the new bytes
	// rather than the version it first opened. Best-effort: if this fails the
	// Diagnostics call below still returns whatever the server last published.
	_ = src.NotifyChange(probeCtx, path)

	diags, err := src.Diagnostics(probeCtx, path)
	if err != nil || len(diags) == 0 {
		return ""
	}

	filtered := make([]lsp.Diagnostic, 0, len(diags))
	for _, d := range diags {
		if d.Severity == lsp.Error || d.Severity == lsp.Warning {
			filtered = append(filtered, d)
		}
	}
	if len(filtered) == 0 {
		return ""
	}

	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].Range.Start.Line != filtered[j].Range.Start.Line {
			return filtered[i].Range.Start.Line < filtered[j].Range.Start.Line
		}
		if filtered[i].Range.Start.Character != filtered[j].Range.Start.Character {
			return filtered[i].Range.Start.Character < filtered[j].Range.Start.Character
		}
		return filtered[i].Message < filtered[j].Message
	})

	errs := 0
	for _, d := range filtered {
		if d.Severity == lsp.Error {
			errs++
		}
	}

	var b strings.Builder
	rel := displayPath(workDir, path)
	if errs > 0 {
		fmt.Fprintf(&b, "Diagnostics after editing %s (%d error(s)) — please fix:\n", rel, errs)
	} else {
		fmt.Fprintf(&b, "Diagnostics after editing %s:\n", rel)
	}

	shown := filtered
	truncated := 0
	if len(shown) > maxPostWriteDiagnostics {
		truncated = len(shown) - maxPostWriteDiagnostics
		shown = shown[:maxPostWriteDiagnostics]
	}
	// Cache file contents so the offending source line can be shown beneath each
	// diagnostic without re-reading a file once per diagnostic it carries.
	lineCache := map[string][]string{}
	for _, d := range shown {
		fmt.Fprintf(
			&b, "%s:%d:%d: %s: %s",
			displayPath(workDir, d.Path),
			d.Range.Start.Line+1,
			d.Range.Start.Character+1,
			severityString(d.Severity),
			d.Message,
		)
		b.WriteString(diagnosticTail(d))
		b.WriteByte('\n')
		// Surface the offending source line indented beneath the message so the
		// model sees the code at fault without a separate view, matching the
		// diagnostics tool and standard conventions. Omitted when the file or line
		// cannot be read.
		if snippet := sourceLine(lineCache, d.Path, d.Range.Start.Line); snippet != "" {
			b.WriteString("    ")
			b.WriteString(snippet)
			b.WriteByte('\n')
		}
	}
	if truncated > 0 {
		fmt.Fprintf(&b, "… and %d more\n", truncated)
	}

	return strings.TrimRight(b.String(), "\n")
}

// displayPath renders path relative to workDir with forward slashes when it
// lives inside the workspace, falling back to the absolute path otherwise.
func displayPath(workDir, path string) string {
	if workDir == "" {
		return path
	}
	rel, err := filepath.Rel(workDir, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return path
	}
	return filepath.ToSlash(rel)
}
