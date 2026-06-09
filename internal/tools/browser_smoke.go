package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// browserSmokeCommandPrefix is the verify candidate emitted for a static HTML
// app (see agent.detectStaticHTML, which proposes "browser-smoke <file>"). It is
// not a real executable: the verify loop recognizes the prefix and routes the
// argument here instead of shelling out. ParseBrowserSmokeCommand does the
// recognition so the agent package does not have to know the spelling.
const browserSmokeCommandPrefix = "browser-smoke"

// ParseBrowserSmokeCommand reports whether command is a browser-smoke verify
// candidate and, when it is, returns the HTML file it targets. The verify loop
// (T9/T10) calls this to decide whether to dispatch to BrowserSmoke rather than
// run the string through a shell, since no "browser-smoke" binary exists.
//
// It matches the exact "browser-smoke <path>" shape detectStaticHTML produces:
// the prefix followed by a single whitespace-separated argument. Anything else
// returns ok=false so a real command of a similar name is left untouched.
func ParseBrowserSmokeCommand(command string) (htmlPath string, ok bool) {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) != 2 || fields[0] != browserSmokeCommandPrefix {
		return "", false
	}
	return fields[1], true
}

// SmokeOptions configures a BrowserSmoke run. The zero value is valid: it auto-
// detects a headless browser and, lacking one, falls back to a static parse.
type SmokeOptions struct {
	// WorkDir resolves a relative HTMLPath. It defaults to the current working
	// directory, matching how verify candidates are produced (a path relative to
	// the repo root).
	WorkDir string
	// BrowserPath forces a specific headless-capable browser binary (Chrome,
	// Chromium, or Edge). When empty BrowserSmoke probes PATH for a known one.
	BrowserPath string
	// DisableBrowser skips the browser entirely and always uses the static parse.
	// It exists for tests and for environments where launching a browser is
	// undesirable even when one is installed.
	DisableBrowser bool
}

// SmokeMethod records which check actually ran, so the report can be honest
// about how much was proven.
type SmokeMethod string

const (
	// SmokeMethodBrowser means the page was loaded in a real headless browser:
	// the strongest available check short of a test suite.
	SmokeMethodBrowser SmokeMethod = "browser"
	// SmokeMethodStatic means only a static DOM/JS parse ran (no browser was
	// available). It proves the document is well-formed and that controls JS
	// reaches for exist, but not that the page runs without runtime errors.
	SmokeMethodStatic SmokeMethod = "static"
)

// SmokeResult is the outcome of a single smoke check. It is deliberately small
// and report-oriented: callers (the verify loop, the agent's final-answer
// builder) want a pass/fail and a concise, actionable reason, not a DOM tree.
type SmokeResult struct {
	// Path is the HTML file that was checked, as given to BrowserSmoke.
	Path string
	// OK is true when the page loaded with no fatal parse error, no detected
	// runtime/console error, and every inferable control present.
	OK bool
	// Method records whether a browser or only a static parse was used.
	Method SmokeMethod
	// Problems lists the concrete failures, one actionable line each. Empty when
	// OK is true.
	Problems []string
	// Controls lists the interactive controls the check verified are present
	// (buttons, form submits, elements JS wires up). It is informational and
	// populated on success as well as failure.
	Controls []string
}

// Report renders a one-line-per-issue summary suitable for a verify failure or
// a final-answer note. On success it states what was proven and how; on failure
// it leads with the count and lists each problem so the model can act on them
// without re-deriving anything.
func (r SmokeResult) Report() string {
	if r.OK {
		how := "static DOM/JS parse"
		if r.Method == SmokeMethodBrowser {
			how = "headless browser load"
		}
		msg := fmt.Sprintf("browser smoke passed (%s): %s loaded without errors", how, r.Path)
		if len(r.Controls) > 0 {
			msg += "; controls present: " + strings.Join(r.Controls, ", ")
		}
		return msg
	}
	var b strings.Builder
	noun := "problem"
	if len(r.Problems) != 1 {
		noun = "problems"
	}
	fmt.Fprintf(&b, "browser smoke failed for %s (%d %s):", r.Path, len(r.Problems), noun)
	for _, p := range r.Problems {
		b.WriteString("\n  - " + p)
	}
	return b.String()
}

// BrowserSmoke loads a static HTML file and reports whether it is healthy enough
// to ship: it parses, has no obvious runtime error, and contains the controls
// its own markup/JS implies. It is the implementation behind the "browser-smoke"
// verify candidate; the agent's verify loop calls it (via ParseBrowserSmokeCommand)
// instead of executing a nonexistent binary.
//
// Strategy, strongest first:
//   - If a headless-capable browser is available (and not disabled), load the
//     page with it and capture any console/JS error. A real load is the only way
//     to catch runtime failures, so this is preferred.
//   - Otherwise fall back to a static parse with golang.org/x/net/html: confirm
//     the file reads, parses as a document, and that controls referenced by id in
//     the page's own scripts actually exist. This cannot catch runtime errors,
//     and SmokeResult.Method says so, but it still turns "I wrote an HTML file"
//     into a checked claim.
//
// The returned error is non-nil only for problems that prevent the check from
// running at all (e.g. the file cannot be read); a page that loads but is broken
// comes back as a SmokeResult with OK=false and a populated Problems list.
func BrowserSmoke(ctx context.Context, htmlPath string, opts SmokeOptions) (SmokeResult, error) {
	abs := htmlPath
	if !filepath.IsAbs(abs) {
		base := opts.WorkDir
		if base == "" {
			base = "."
		}
		abs = filepath.Join(base, htmlPath)
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		// The file cannot even be opened: there is nothing to smoke. This is a
		// setup error, not a page defect, so it is returned as err.
		return SmokeResult{Path: htmlPath, Method: SmokeMethodStatic}, fmt.Errorf("reading %s: %w", htmlPath, err)
	}
	source := string(data)

	if !opts.DisableBrowser {
		if bin := resolveHeadlessBrowser(opts.BrowserPath); bin != "" {
			if res, ok := browserLoad(ctx, bin, abs, source); ok {
				return res, nil
			}
			// The browser was present but the launch failed (sandbox, missing
			// libs, timeout). Don't fail the artifact for our own tooling gap:
			// fall through to the static parse so the user still gets a check.
		}
	}

	return staticSmoke(htmlPath, source), nil
}

// staticSmoke runs the no-browser fallback: parse the document and verify the
// controls its own scripts reference. It never errors — a malformed document is
// a page problem, recorded in the result, not a failure of the check.
func staticSmoke(htmlPath, source string) SmokeResult {
	res := SmokeResult{Path: htmlPath, Method: SmokeMethodStatic}

	doc, err := html.Parse(strings.NewReader(source))
	if err != nil {
		// html.Parse is famously lenient (it mirrors a browser's error recovery),
		// so an actual error here means the input is badly broken — not real HTML.
		res.Problems = append(res.Problems, "document did not parse as HTML: "+err.Error())
		return res
	}

	// An empty or marker-free file is almost never an intended app. Catch the
	// "wrote a placeholder, called it done" case before inspecting controls.
	if !looksLikeHTML(source) {
		res.Problems = append(res.Problems, "file contains no recognizable HTML structure (no <html>, <body>, or element tags)")
		return res
	}

	ids := elementIDs(doc)
	controls := interactiveControls(doc)
	res.Controls = controls

	// Verify every id the page's own scripts reach for actually exists. This is
	// the cheap static analogue of "click the button and see if it's wired up":
	// a getElementById("save") with no #save element is a guaranteed runtime
	// no-op, and the most common bug in hand-written single-file apps. Only live
	// <script> code is scanned (comments and string literals stripped) so an id
	// named in a comment or quoted in docs is not mistaken for a real reference.
	for _, ref := range referencedIDs(scriptCode(doc)) {
		if !ids[ref] {
			res.Problems = append(res.Problems,
				fmt.Sprintf("script references #%s but no element with that id exists", ref))
		}
	}

	res.OK = len(res.Problems) == 0
	return res
}

// browserSmokeTimeout bounds a single headless render. Chromium's --dump-dom
// writes the DOM and then, on some builds, does not self-terminate; this cap
// kills the process once the DOM has had time to materialize so the check stays
// bounded even when the caller passes a context.Background() with no deadline.
const browserSmokeTimeout = 8 * time.Second

// browserLoad drives bin in headless mode to render abs and dump the resulting
// DOM, treating any captured JS/console error as a page problem. It returns
// ok=false when the browser could not be driven at all (so the caller can fall
// back to the static parse); a page that loads but errors comes back ok=true
// with OK=false on the result.
//
// It bounds the run with its own timeout (browserSmokeTimeout) derived from the
// caller ctx, because some Chromium builds dump the DOM under --dump-dom but then
// hang instead of exiting. When that timeout kills the process we still have a
// populated DOM and any console output captured before the kill, so the result
// is built from those rather than discarded — only a run that produced no DOM and
// no usable stderr is treated as "could not run".
func browserLoad(ctx context.Context, bin, abs, source string) (SmokeResult, bool) {
	res := SmokeResult{Path: filepathBase(abs), Method: SmokeMethodBrowser}

	// --dump-dom prints the post-script DOM to stdout; --enable-logging=stderr
	// with v=1 surfaces page console errors and uncaught exceptions on stderr.
	// The temp user-data-dir keeps the run from touching the user's profile.
	tmp, err := os.MkdirTemp("", "bharatcode-smoke-")
	if err != nil {
		return res, false
	}
	defer os.RemoveAll(tmp)

	// An internal timeout bounds a browser that renders but will not exit. It is
	// derived from the caller ctx so a real cancellation still propagates, but it
	// fires on its own even when the caller has no deadline.
	runCtx, cancel := context.WithTimeout(ctx, browserSmokeTimeout)
	defer cancel()

	args := []string{
		"--headless=new",
		"--disable-gpu",
		"--no-sandbox",
		"--user-data-dir=" + tmp,
		"--enable-logging=stderr",
		"--v=1",
		"--dump-dom",
		"file://" + abs,
	}
	cmd := commandContext(runCtx, bin, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, runErr := cmd.Output()

	// Distinguish a caller-initiated cancellation from our own render timeout. A
	// caller cancellation is not a verdict on the page, so bail to the fallback.
	if ctx.Err() != nil {
		return res, false
	}

	// Uncaught page exceptions land on stderr; browserErrorLines keeps only those
	// (and structured error-severity records), discarding the INFO/WARNING noise
	// and the browser-internal :ERROR: lines --enable-logging emits on every run.
	errLines := browserErrorLines(stderr.String())

	// A run that produced no DOM and no error lines proved nothing: the browser
	// crashed, hit a bad flag, or was missing a lib before rendering. Fall back
	// to the static parse. Anything else — a populated DOM, or error lines worth
	// reporting — is a real verdict, even when our render timeout had to kill a
	// browser that dumped the DOM but would not exit on its own.
	if runErr != nil && len(out) == 0 && len(errLines) == 0 {
		return res, false
	}

	for _, line := range errLines {
		res.Problems = append(res.Problems, "console error: "+line)
	}

	// Use the rendered DOM (post-script) for control inference so dynamically
	// added controls count; fall back to the source when the dump is empty.
	domSource := string(out)
	if strings.TrimSpace(domSource) == "" {
		domSource = source
	}
	if doc, perr := html.Parse(strings.NewReader(domSource)); perr == nil {
		res.Controls = interactiveControls(doc)
	}

	res.OK = len(res.Problems) == 0
	return res, true
}

// resolveHeadlessBrowser returns the path to a headless-capable browser. An
// explicit preferred path wins when it resolves; otherwise PATH is probed for
// the common Chrome/Chromium/Edge binary names. It returns "" when none is
// found, which is the signal to use the static fallback.
func resolveHeadlessBrowser(preferred string) string {
	if preferred != "" {
		if p, err := lookPath(preferred); err == nil {
			return p
		}
		return ""
	}
	for _, name := range headlessBrowserNames {
		if p, err := lookPath(name); err == nil {
			return p
		}
	}
	return ""
}

// headlessBrowserNames are the executables, in preference order, that accept
// Chromium's --headless/--dump-dom flags. macOS app-bundle binaries are listed
// by their absolute path since they are not on PATH; lookPath returns them
// verbatim when they exist.
var headlessBrowserNames = []string{
	"google-chrome",
	"google-chrome-stable",
	"chromium",
	"chromium-browser",
	"microsoft-edge",
	"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
	"/Applications/Chromium.app/Contents/MacOS/Chromium",
	"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
}

// browserErrorRe matches only the page-level failures Chromium writes to stderr
// under --enable-logging=stderr: an uncaught exception (which always carries the
// word "Uncaught", e.g. an "[...:CONSOLE:NN] Uncaught ReferenceError ..." line),
// or a structured error-severity record. It deliberately does NOT match:
//   - bare "[...:ERROR:foo.cc:NN]" lines, which are browser-internal subsystem
//     errors (GCM registration, crashpad settings, macOS task-policy) emitted on
//     every headless launch and unrelated to the page; and
//   - plain CONSOLE lines, because console.log / console.warn / console.error
//     all log at the same INFO:CONSOLE tag, so matching the tag would fail a page
//     for benign logging.
//
// Anchoring on "Uncaught" keeps the check to genuine runtime breakage — the case
// the browser path exists to catch — without false-failing a noisy healthy page.
var browserErrorRe = regexp.MustCompile(`(?i)"severity":\s*"error"|\bUncaught\b`)

// browserErrorLines extracts the distinct error-level lines from a browser's
// stderr, trimmed for reporting. Duplicates are collapsed so a control that
// errors once per frame does not flood the report.
func browserErrorLines(stderr string) []string {
	seen := map[string]bool{}
	var out []string
	for _, ln := range strings.Split(stderr, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || !browserErrorRe.MatchString(ln) {
			continue
		}
		if seen[ln] {
			continue
		}
		seen[ln] = true
		out = append(out, ln)
	}
	return out
}

// elementIDs collects every id attribute in the document into a set, used to
// confirm that ids the page's scripts reference actually exist.
func elementIDs(n *html.Node) map[string]bool {
	ids := map[string]bool{}
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode {
			for _, a := range node.Attr {
				if a.Key == "id" && a.Val != "" {
					ids[a.Val] = true
				}
			}
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return ids
}

// interactiveControls returns a sorted, de-duplicated list of the interactive
// controls present in the document, labeled for the report (e.g. "button#save",
// "form submit", "input[type=text]"). It is what makes the smoke check "control
// aware": the report can state which controls it confirmed exist.
func interactiveControls(n *html.Node) []string {
	seen := map[string]bool{}
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode {
			if label := controlLabel(node); label != "" && !seen[label] {
				seen[label] = true
			}
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	out := make([]string, 0, len(seen))
	for label := range seen {
		out = append(out, label)
	}
	sort.Strings(out)
	return out
}

// controlLabel returns a short label for an interactive element, or "" when the
// node is not a control worth reporting. It prefers an id ("button#save") so the
// label is specific, falling back to a type-qualified tag.
func controlLabel(node *html.Node) string {
	tag := node.Data
	id := attr(node, "id")
	switch tag {
	case "button":
		return withID("button", id)
	case "a":
		// Only treat links with an href as navigational controls.
		if attr(node, "href") != "" {
			return withID("link", id)
		}
	case "select", "textarea":
		return withID(tag, id)
	case "input":
		typ := strings.ToLower(attr(node, "type"))
		if typ == "" {
			typ = "text"
		}
		if typ == "hidden" {
			return ""
		}
		if typ == "submit" || typ == "button" {
			return withID("button("+typ+")", id)
		}
		return withID("input["+typ+"]", id)
	case "form":
		return withID("form", id)
	}
	return ""
}

// withID joins a control kind with its id when one is present, producing a
// label like "button#save"; without an id it returns the kind alone.
func withID(kind, id string) string {
	if id == "" {
		return kind
	}
	return kind + "#" + id
}

// attr returns the value of the named attribute on node, or "" when absent.
func attr(node *html.Node, key string) string {
	for _, a := range node.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// idRefRe matches the element-id arguments the page's scripts hand to the DOM
// lookups that silently return null on a miss: getElementById and the id-form of
// querySelector ("#foo"). These are the references whose absence is a real, and
// invisible, bug — so they are exactly what the static check confirms.
var idRefRe = regexp.MustCompile(`getElementById\(\s*["']([A-Za-z][\w\-:.]*)["']\s*\)|querySelector\(\s*["']#([A-Za-z][\w\-:.]*)["']\s*\)`)

// scriptCode walks the parsed document, concatenates the text of every inline
// <script> element, and strips the parts of that code that cannot hold a live
// DOM reference: // line comments, /* */ block comments, and the contents of
// quoted string literals. The stripped result is what referencedIDs scans, so
// only ids the running script actually looks up are checked — a getElementById
// mentioned in a comment, or an id quoted inside an unrelated string, is ignored.
func scriptCode(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "script" {
			// Skip external scripts (src=...) and non-JS types; their body, if any,
			// is data the browser does not execute as JS.
			if src := attr(node, "src"); src != "" {
				return
			}
			if typ := strings.ToLower(strings.TrimSpace(attr(node, "type"))); typ != "" &&
				typ != "text/javascript" && typ != "application/javascript" && typ != "module" {
				return
			}
			for c := node.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == html.TextNode {
					b.WriteString(stripJSNonCode(c.Data))
					b.WriteByte('\n')
				}
			}
			return
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

// idCallTailRe matches code that ends with an id-lookup call awaiting its quoted
// argument — getElementById( or querySelector( with optional whitespace before
// the opening quote. stripJSNonCode uses it to tell an id-argument string (whose
// content must survive for idRefRe) apart from any other string literal.
var idCallTailRe = regexp.MustCompile(`(?:getElementById|querySelector)\(\s*$`)

// stripJSNonCode removes line comments, block comments, and the contents of
// string literals from a piece of JavaScript, so that a getElementById or
// querySelector mention buried in a comment or quoted inside an unrelated string
// is not mistaken for a live DOM reference. It is a lightweight scanner, not a
// real JS parser: it tracks just enough state (which quote/comment it is inside)
// to blank the non-code spans.
//
// The one string whose content is preserved is the immediate argument of an
// id-lookup call — the "save" in getElementById("save") — because that literal
// is exactly what referencedIDs must read. Quote and comment delimiters are kept
// either way so the surrounding call syntax stays intact for the regex.
func stripJSNonCode(js string) string {
	var b strings.Builder
	const (
		code = iota
		lineComment
		blockComment
		single // '...'
		double // "..."
		tmpl   // `...`
	)
	state := code
	// keepArg is set when the string just opened is the argument of an id-lookup
	// call, so its content is written through instead of dropped.
	keepArg := false
	for i := 0; i < len(js); i++ {
		c := js[i]
		switch state {
		case code:
			switch {
			case c == '/' && i+1 < len(js) && js[i+1] == '/':
				state = lineComment
				i++
			case c == '/' && i+1 < len(js) && js[i+1] == '*':
				state = blockComment
				i++
			case c == '\'' || c == '"' || c == '`':
				// A string opens here. It is an id argument worth preserving only
				// when the code emitted so far ends with getElementById(/querySelector(.
				keepArg = c != '`' && idCallTailRe.MatchString(b.String())
				switch c {
				case '\'':
					state = single
				case '"':
					state = double
				default:
					state = tmpl
				}
				b.WriteByte(c)
			default:
				b.WriteByte(c)
			}
		case lineComment:
			if c == '\n' {
				state = code
				b.WriteByte(c)
			}
		case blockComment:
			if c == '*' && i+1 < len(js) && js[i+1] == '/' {
				state = code
				i++
			}
		case single, double, tmpl:
			// A backslash escapes the next character, including the closing quote.
			if c == '\\' && i+1 < len(js) {
				if keepArg {
					b.WriteByte(c)
					b.WriteByte(js[i+1])
				}
				i++
				continue
			}
			if (state == single && c == '\'') ||
				(state == double && c == '"') ||
				(state == tmpl && c == '`') {
				state = code
				keepArg = false
				b.WriteByte(c)
				continue
			}
			if keepArg {
				b.WriteByte(c)
			}
		}
	}
	return b.String()
}

// referencedIDs returns the distinct element ids the page's inline scripts look
// up by id. It expects already-isolated script code (see scriptCode), with
// comments and string literals stripped, so that a getElementById call mentioned
// in a comment, a quoted string, or a <pre>/<code> doc block is not counted as a
// live reference and does not fail an otherwise-correct page.
func referencedIDs(scriptText string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range idRefRe.FindAllStringSubmatch(scriptText, -1) {
		// Group 1 is the getElementById arg; group 2 the querySelector("#..") arg.
		id := m[1]
		if id == "" {
			id = m[2]
		}
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// filepathBase returns the final path element, used to report the page by its
// name when only an absolute path is on hand.
func filepathBase(p string) string {
	return filepath.Base(p)
}
