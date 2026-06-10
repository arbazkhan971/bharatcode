package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/util"
	"github.com/arbazkhan971/bharatcode/internal/util/fsext"
	"github.com/spf13/cobra"
)

type rootOptionsKey struct{}

func withRootOptions(ctx context.Context, opts *rootOptions) context.Context {
	return context.WithValue(ctx, rootOptionsKey{}, opts)
}

func getRootOptions(cmd *cobra.Command) *rootOptions {
	if opts, ok := cmd.Context().Value(rootOptionsKey{}).(*rootOptions); ok && opts != nil {
		return opts
	}
	if parent := cmd.Root(); parent != nil && parent != cmd {
		return getRootOptions(parent)
	}
	return &rootOptions{}
}

func canonicalProjectPath(projectDir string) (string, error) {
	project := projectDir
	if project == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("getting current directory: %w", err)
		}
		project = cwd
	}
	project = util.ExpandPath(project)
	abs, err := filepath.Abs(project)
	if err != nil {
		return "", fmt.Errorf("resolving project directory %q: %w", projectDir, err)
	}
	return filepath.Clean(abs), nil
}

func executeCommand(ctx context.Context, cmd *cobra.Command) error {
	if cmd.Context() == nil {
		cmd.SetContext(ctx)
	}
	err := cmd.ExecuteContext(cmd.Context())
	if err == nil {
		return nil
	}
	printError(cmd.ErrOrStderr(), err)
	return err
}

func printError(w io.Writer, err error) {
	if err == nil {
		return
	}
	_, _ = fmt.Fprintf(w, "Error: %s\n", err.Error())
}

func renderTable(rows [][]string) string {
	if len(rows) == 0 {
		return ""
	}
	widths := make([]int, len(rows[0]))
	for _, row := range rows {
		for i, col := range row {
			if len(col) > widths[i] {
				widths[i] = len(col)
			}
		}
	}
	var buf strings.Builder
	for _, row := range rows {
		for i, col := range row {
			if i > 0 {
				buf.WriteString("  ")
			}
			buf.WriteString(col)
			if i < len(row)-1 {
				buf.WriteString(strings.Repeat(" ", widths[i]-len(col)))
			}
		}
		buf.WriteByte('\n')
	}
	return buf.String()
}

func loadConfig(ctx context.Context, opts *rootOptions) (*config.Config, string, error) {
	path := opts.configPath
	if path == "" {
		path = config.GlobalPath()
	}
	project := ""
	if opts.projectDir != "" {
		project = config.ProjectPath(opts.projectDir)
	}
	cfg, err := config.LoadFrom(ctx, path, project)
	if err != nil {
		return nil, "", fmt.Errorf("loading config: %w", err)
	}
	return cfg, path, nil
}

func saveConfigPath(ctx context.Context, path string, cfg *config.Config) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}
	if path == "" {
		path = config.GlobalPath()
	}
	path = util.ExpandPath(path)
	if err := fsext.EnsureDir(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("ensuring config directory: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	data = append(data, '\n')
	if err := fsext.AtomicWrite(path, data, 0o600); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	return nil
}

func readPrompt(cmd *cobra.Command, args []string) (string, error) {
	if len(args) > 0 {
		return strings.Join(args, " "), nil
	}
	if input, ok := cmd.InOrStdin().(*os.File); ok {
		stat, err := input.Stat()
		if err != nil {
			return "", fmt.Errorf("checking stdin: %w", err)
		}
		if stat.Mode()&os.ModeCharDevice != 0 {
			return "", fmt.Errorf("prompt required")
		}
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, cmd.InOrStdin()); err != nil {
		return "", fmt.Errorf("reading prompt from stdin: %w", err)
	}
	prompt := strings.TrimSpace(buf.String())
	if prompt == "" {
		return "", fmt.Errorf("prompt required")
	}
	return prompt, nil
}

func readSecret(cmd *cobra.Command, label string) (string, error) {
	reader := bufio.NewReader(cmd.InOrStdin())
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s: ", label)
	value, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("reading token: %w", err)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("token required")
	}
	return value, nil
}

func formatRupees(n float64) string {
	if n == float64(int64(n)) {
		return strconv.FormatInt(int64(n), 10)
	}
	return strconv.FormatFloat(n, 'f', 2, 64)
}

func defaultEditor() string {
	if editor := os.Getenv("EDITOR"); editor != "" {
		return editor
	}
	if runtime.GOOS == "windows" {
		return "notepad.exe"
	}
	return "vi"
}

func parseSince(value string) (time.Time, error) {
	now := time.Now()
	switch value {
	case "", "30d":
		return now.AddDate(0, 0, -30), nil
	case "7d":
		return now.AddDate(0, 0, -7), nil
	case "month":
		return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()), nil
	case "all":
		return time.Time{}, nil
	default:
		return time.Time{}, fmt.Errorf("invalid --since %q", value)
	}
}
