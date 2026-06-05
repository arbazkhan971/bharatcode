package tools

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/util"
)

const (
	defaultMaxToolOutputBytes = 32 * 1024
	maxViewImageBytes         = 5 * 1024 * 1024
)

type viewedFiles struct {
	mu    sync.RWMutex
	paths map[string]struct{}
}

func (v *viewedFiles) mark(path string) {
	if v == nil {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.paths == nil {
		v.paths = make(map[string]struct{})
	}
	v.paths[path] = struct{}{}
}

func (v *viewedFiles) has(path string) bool {
	if v == nil {
		return false
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	_, ok := v.paths[path]
	return ok
}

var sessionViews sync.Map

// ViewTool reads a workspace file and returns numbered text or image metadata.
type ViewTool struct {
	deps Dependencies
}

//go:embed view.md
var viewDescription string

var viewSchema = json.RawMessage(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["path"],
  "properties": {
    "path": {"type": "string", "minLength": 1},
    "offset": {"type": "integer", "minimum": 1},
    "limit": {"type": "integer", "minimum": 1}
  }
}`)

// newViewTool constructs the workspace file reader.
func newViewTool(deps Dependencies) *ViewTool {
	return &ViewTool{deps: deps}
}

// Name returns the tool name.
func (t *ViewTool) Name() string { return "view" }

// Description returns the model-facing tool description.
func (t *ViewTool) Description() string { return viewDescription }

// Schema returns the JSON argument schema.
func (t *ViewTool) Schema() json.RawMessage { return viewSchema }

// Run executes the view tool.
func (t *ViewTool) Run(ctx context.Context, args json.RawMessage) (res Result, err error) {
	defer recoverFSTool(&res, &err)

	var in struct {
		Path   string `json:"path"`
		Offset int    `json:"offset,omitempty"`
		Limit  int    `json:"limit,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return errorResult("invalid JSON arguments: " + err.Error()), nil
	}
	if strings.TrimSpace(in.Path) == "" {
		return errorResult("path is required"), nil
	}

	path, err := resolveToolPath(in.Path, t.deps.WorkDir)
	if err != nil {
		return errorResult(err.Error()), nil
	}
	if !isInsideWorkDir(path, t.deps.WorkDir) && !isAllowedReadPath(path, t.deps.Config) {
		return errorResult("path is outside the workspace: " + path), nil
	}

	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errorResult("file does not exist: " + path), nil
		}
		return Result{}, fmt.Errorf("stating file %s: %w", path, err)
	}
	if info.IsDir() {
		return errorResult("path is a directory: " + path), nil
	}

	if isImagePath(path) {
		return t.viewImage(ctx, path, info.Size())
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return Result{}, fmt.Errorf("reading file %s: %w", path, err)
	}
	if !utf8.Valid(data) {
		return errorResult("file is not valid UTF-8 text: " + path), nil
	}
	if err := t.recordRead(ctx, path); err != nil {
		return Result{}, err
	}

	content, span := numberedLines(string(data), in.Offset, in.Limit)
	content = truncateContent(content, span, maxToolOutputBytes(t.deps.Config))
	return Result{
		Content: content,
		Metadata: map[string]any{
			"path": path,
		},
	}, nil
}

func (t *ViewTool) viewImage(ctx context.Context, path string, size int64) (Result, error) {
	if size > maxViewImageBytes {
		return errorResult("image too large"), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Result{}, fmt.Errorf("reading image %s: %w", path, err)
	}
	if err := t.recordRead(ctx, path); err != nil {
		return Result{}, err
	}
	mimeType := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	return Result{
		Content: "image file: " + path,
		Metadata: map[string]any{
			"path":           path,
			MetadataImage:    base64.StdEncoding.EncodeToString(data),
			MetadataMimeType: mimeType,
		},
	}, nil
}

func (t *ViewTool) recordRead(ctx context.Context, path string) error {
	if t.deps.FileTracker != nil && t.deps.SessionID != "" {
		if err := t.deps.FileTracker.RecordRead(ctx, t.deps.SessionID, path); err != nil {
			return fmt.Errorf("recording read for %s: %w", path, err)
		}
	}
	markViewed(t.deps.SessionID, path)
	return nil
}

func markViewed(sessionID, path string) {
	views := viewsForSession(sessionID)
	views.mark(path)
}

func hasViewed(sessionID, path string) bool {
	views := viewsForSession(sessionID)
	return views.has(path)
}

func viewsForSession(sessionID string) *viewedFiles {
	key := sessionID
	if key == "" {
		key = "__default__"
	}
	value, _ := sessionViews.LoadOrStore(key, &viewedFiles{paths: make(map[string]struct{})})
	return value.(*viewedFiles)
}

func recoverFSTool(res *Result, err *error) {
	if r := recover(); r != nil {
		*res = errorResult(fmt.Sprintf("internal tool panic: %v", r))
		*err = nil
	}
}

func resolveToolPath(input, workDir string) (string, error) {
	expanded := util.ExpandPath(input)
	if expanded == "" {
		return "", fmt.Errorf("path is required")
	}
	if !filepath.IsAbs(expanded) {
		base := util.ExpandPath(workDir)
		if base == "" {
			var err error
			base, err = os.Getwd()
			if err != nil {
				return "", fmt.Errorf("getting current directory: %w", err)
			}
		}
		expanded = filepath.Join(base, expanded)
	}
	abs, err := filepath.Abs(expanded)
	if err != nil {
		return "", fmt.Errorf("resolving path %q: %w", input, err)
	}
	return filepath.Clean(abs), nil
}

func cleanWorkDir(workDir string) string {
	expanded := util.ExpandPath(workDir)
	if expanded == "" {
		return ""
	}
	abs, err := filepath.Abs(expanded)
	if err != nil {
		return filepath.Clean(expanded)
	}
	return filepath.Clean(abs)
}

func isInsideWorkDir(path, workDir string) bool {
	root := cleanWorkDir(workDir)
	if root == "" {
		return true
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func isAllowedReadPath(path string, cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	values := stringSliceField(reflect.ValueOf(cfg), "AllowedReadPaths")
	for _, allowed := range values {
		resolved, err := resolveToolPath(allowed, "")
		if err != nil || resolved == "" {
			continue
		}
		if path == resolved || isInsideWorkDir(path, resolved) {
			return true
		}
	}
	return false
}

func stringSliceField(v reflect.Value, name string) []string {
	if !v.IsValid() {
		return nil
	}
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil
	}
	field := v.FieldByName(name)
	if !field.IsValid() {
		for i := 0; i < v.NumField(); i++ {
			out := stringSliceField(fieldValue(v.Field(i)), name)
			if len(out) > 0 {
				return out
			}
		}
		return nil
	}
	return valueAsStringSlice(field)
}

func fieldValue(v reflect.Value) reflect.Value {
	if v.Kind() == reflect.Pointer && v.IsNil() {
		return reflect.Value{}
	}
	return v
}

func valueAsStringSlice(v reflect.Value) []string {
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Slice || v.Type().Elem().Kind() != reflect.String {
		return nil
	}
	out := make([]string, v.Len())
	for i := 0; i < v.Len(); i++ {
		out[i] = v.Index(i).String()
	}
	return out
}

// lineSpan describes which file lines a rendered view covers. All values are
// one-based and total reflects the file's full line count, so a truncation
// marker can advertise an accurate continuation offset.
type lineSpan struct {
	firstLine int
	lastLine  int
	total     int
}

func numberedLines(content string, offset, limit int) (string, lineSpan) {
	lines := strings.Split(content, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	total := len(lines)
	if offset <= 0 {
		offset = 1
	}
	if offset > total {
		return "", lineSpan{firstLine: offset, lastLine: offset - 1, total: total}
	}
	start := offset - 1
	end := total
	if limit > 0 && start+limit < end {
		end = start + limit
	}

	width := len(strconv.Itoa(end))
	var b strings.Builder
	for i := start; i < end; i++ {
		b.WriteString(fmt.Sprintf("%*d | %s", width, i+1, lines[i]))
		if i < end-1 {
			b.WriteByte('\n')
		}
	}
	return b.String(), lineSpan{firstLine: start + 1, lastLine: end, total: total}
}

// truncateContent caps numbered output at maxBytes and replaces the dead-end
// byte count with an actionable marker: it tells the model which lines were
// shown and the offset to pass to view to continue paging. When a single line
// is itself larger than the budget it cannot be paged with offset, so a
// concrete shell fallback is suggested instead. The rune-boundary safety of the
// cut is preserved.
func truncateContent(content string, span lineSpan, maxBytes int) string {
	if maxBytes <= 0 || len(content) <= maxBytes {
		return content
	}

	cut := maxBytes
	if cut > len(content) {
		cut = len(content)
	}
	// Prefer to cut on a line boundary so the continuation offset lands on a
	// whole line rather than mid-line.
	if nl := strings.LastIndexByte(content[:cut], '\n'); nl >= 0 {
		cut = nl
	} else {
		cut = 0
	}
	// Back off to a valid rune boundary in case the line itself is multibyte.
	for cut > 0 && !utf8.ValidString(content[:cut]) {
		cut--
	}

	shown := content[:cut]
	// Number of complete numbered lines kept before the cut.
	kept := 0
	if shown != "" {
		kept = strings.Count(shown, "\n") + 1
	}
	nextLine := span.firstLine + kept

	if kept == 0 {
		// The first line alone exceeds the budget; offset paging cannot help, so
		// point at a shell fallback that streams just that line's bytes.
		return fmt.Sprintf(
			"[Line %d exceeds the %d-byte view limit; use bash: sed -n %q <path> | head -c %d]",
			span.firstLine, maxBytes, fmt.Sprintf("%dp", span.firstLine), maxBytes,
		)
	}

	lastShown := nextLine - 1
	return shown + fmt.Sprintf(
		"\n[Showing lines %d-%d of %d. Use offset=%d to continue.]",
		span.firstLine, lastShown, span.total, nextLine,
	)
}

func maxToolOutputBytes(cfg *config.Config) int {
	if cfg == nil {
		return defaultMaxToolOutputBytes
	}
	if n := intField(reflect.ValueOf(cfg), "MaxToolOutputBytes"); n > 0 {
		return n
	}
	return defaultMaxToolOutputBytes
}

func intField(v reflect.Value, name string) int {
	if !v.IsValid() {
		return 0
	}
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return 0
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return 0
	}
	field := v.FieldByName(name)
	if field.IsValid() {
		switch field.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return int(field.Int())
		default:
			return 0
		}
	}
	for i := 0; i < v.NumField(); i++ {
		if n := intField(fieldValue(v.Field(i)), name); n > 0 {
			return n
		}
	}
	return 0
}

func isImagePath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	default:
		return false
	}
}
