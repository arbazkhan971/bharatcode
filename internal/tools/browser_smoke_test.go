package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseBrowserSmokeCommand(t *testing.T) {
	cases := []struct {
		command  string
		wantPath string
		wantOK   bool
	}{
		{"browser-smoke index.html", "index.html", true},
		{"  browser-smoke   app.html  ", "app.html", true},
		{"browser-smoke", "", false},               // no argument
		{"browser-smoke a.html b.html", "", false}, // ambiguous extra arg
		{"go test ./...", "", false},               // unrelated command
		{"my-browser-smoke index.html", "", false}, // not the exact prefix
	}
	for _, tc := range cases {
		path, ok := ParseBrowserSmokeCommand(tc.command)
		if ok != tc.wantOK || path != tc.wantPath {
			t.Errorf("ParseBrowserSmokeCommand(%q) = (%q, %v), want (%q, %v)",
				tc.command, path, ok, tc.wantPath, tc.wantOK)
		}
	}
}

// writeHTML drops content into a temp dir and returns the dir and file name.
func writeHTML(t *testing.T, content string) (dir, name string) {
	t.Helper()
	dir = t.TempDir()
	name = "index.html"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	return dir, name
}

func TestBrowserSmokeStaticPasses(t *testing.T) {
	const page = `<!doctype html><html><body>
<button id="save">Save</button>
<form id="login"><input type="text" id="user"><input type="submit"></form>
<script>document.getElementById('save').onclick = function(){};</script>
</body></html>`
	dir, name := writeHTML(t, page)

	res, err := BrowserSmoke(context.Background(), name, SmokeOptions{WorkDir: dir, DisableBrowser: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.OK {
		t.Fatalf("expected pass, got problems: %v", res.Problems)
	}
	if res.Method != SmokeMethodStatic {
		t.Fatalf("expected static method, got %q", res.Method)
	}
	// The save button, the form, and its submit/text inputs should all be
	// reported as confirmed controls.
	joined := strings.Join(res.Controls, ",")
	for _, want := range []string{"button#save", "form#login", "button(submit)", "input[text]#user"} {
		if !strings.Contains(joined, want) {
			t.Errorf("control %q not reported; got %v", want, res.Controls)
		}
	}
	if !strings.Contains(res.Report(), "passed") {
		t.Errorf("success report should say passed: %q", res.Report())
	}
}

func TestBrowserSmokeStaticDetectsMissingControl(t *testing.T) {
	// The script wires up #save, but there is no element with that id: a silent
	// runtime no-op the static check must catch.
	const page = `<!doctype html><html><body>
<button id="cancel">Cancel</button>
<script>document.getElementById('save').addEventListener('click', run);</script>
</body></html>`
	dir, name := writeHTML(t, page)

	res, err := BrowserSmoke(context.Background(), name, SmokeOptions{WorkDir: dir, DisableBrowser: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.OK {
		t.Fatalf("expected failure for missing #save element")
	}
	if len(res.Problems) != 1 || !strings.Contains(res.Problems[0], "#save") {
		t.Fatalf("expected one problem naming #save, got %v", res.Problems)
	}
	rep := res.Report()
	if !strings.Contains(rep, "failed") || !strings.Contains(rep, "#save") {
		t.Errorf("failure report should be actionable: %q", rep)
	}
}

func TestBrowserSmokeStaticQuerySelectorMiss(t *testing.T) {
	// querySelector("#id") has the same null-on-miss footgun as getElementById.
	const page = `<!doctype html><html><body><div id="present"></div>
<script>const el = document.querySelector('#missing'); el.focus();</script>
</body></html>`
	dir, name := writeHTML(t, page)

	res, _ := BrowserSmoke(context.Background(), name, SmokeOptions{WorkDir: dir, DisableBrowser: true})
	if res.OK {
		t.Fatalf("expected failure for querySelector('#missing')")
	}
	if len(res.Problems) != 1 || !strings.Contains(res.Problems[0], "#missing") {
		t.Fatalf("expected problem naming #missing, got %v", res.Problems)
	}
}

func TestBrowserSmokeIgnoresCommentedAndQuotedRefs(t *testing.T) {
	// The only mentions of #save and #legacy are in a line comment, a block
	// comment, and a string literal — none of them run, so a correct page must
	// not be failed for them. The single live reference (#present) resolves.
	const page = `<!doctype html><html><body>
<div id="present"></div>
<script>
// document.getElementById('save') is intentionally disabled for now
/* old: document.querySelector('#legacy').focus(); */
const note = "see getElementById('legacy') docs";
document.getElementById('present').textContent = note;
</script>
</body></html>`
	dir, name := writeHTML(t, page)

	res, err := BrowserSmoke(context.Background(), name, SmokeOptions{WorkDir: dir, DisableBrowser: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.OK {
		t.Fatalf("expected pass; commented/quoted refs should be ignored, got problems: %v", res.Problems)
	}
}

func TestBrowserSmokeDetectsLiveRefAmongComments(t *testing.T) {
	// A real getElementById('save') with no #save element must still be caught
	// even when other id mentions are buried in comments and strings around it.
	const page = `<!doctype html><html><body>
<div id="present"></div>
<script>
// helper: document.getElementById('ignored') would be a no-op here
const label = "querySelector('#also-ignored')";
document.getElementById('save').addEventListener('click', run);
</script>
</body></html>`
	dir, name := writeHTML(t, page)

	res, _ := BrowserSmoke(context.Background(), name, SmokeOptions{WorkDir: dir, DisableBrowser: true})
	if res.OK {
		t.Fatalf("expected failure for live #save reference")
	}
	if len(res.Problems) != 1 || !strings.Contains(res.Problems[0], "#save") {
		t.Fatalf("expected exactly one problem naming #save, got %v", res.Problems)
	}
}

func TestBrowserSmokeIgnoresExternalAndNonJSScripts(t *testing.T) {
	// References in an external <script src> body or a non-JS <script type> are
	// not executed as page JS, so they must not be scanned for id lookups.
	const page = `<!doctype html><html><body>
<div id="present"></div>
<script src="app.js">document.getElementById('external')</script>
<script type="application/json">{"q": "getElementById('json')"}</script>
<script>document.getElementById('present').focus();</script>
</body></html>`
	dir, name := writeHTML(t, page)

	res, _ := BrowserSmoke(context.Background(), name, SmokeOptions{WorkDir: dir, DisableBrowser: true})
	if !res.OK {
		t.Fatalf("expected pass; non-executed script bodies should be ignored, got: %v", res.Problems)
	}
}

func TestStripJSNonCode(t *testing.T) {
	cases := []struct {
		name string
		in   string
		// want is a substring that must survive (live code) or, when wantGone is
		// set, a substring that must NOT survive (stripped non-code).
		want     string
		wantGone string
	}{
		{"line comment dropped", "a // getElementById('x')\nb", "wantGone:getElementById", ""},
		{"block comment dropped", "a /* getElementById('x') */ b", "", "getElementById"},
		{"string contents dropped", `var s = "getElementById('x')"; run()`, "run()", "getElementById"},
		{"template literal dropped", "var s = `getElementById('x')`; go()", "go()", "getElementById"},
		{"escaped quote stays in string", `"a\"b getElementById('x')"; keep()`, "keep()", "getElementById"},
		{"live code survives", "document.getElementById('save')", "getElementById('save')", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := stripJSNonCode(tc.in)
			if tc.wantGone != "" && strings.Contains(got, tc.wantGone) {
				t.Errorf("stripJSNonCode(%q) = %q, should not contain %q", tc.in, got, tc.wantGone)
			}
			if w := strings.TrimPrefix(tc.want, "wantGone:"); w != tc.want {
				if strings.Contains(got, w) {
					t.Errorf("stripJSNonCode(%q) = %q, should not contain %q", tc.in, got, w)
				}
			} else if tc.want != "" && !strings.Contains(got, tc.want) {
				t.Errorf("stripJSNonCode(%q) = %q, want it to contain %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestBrowserSmokeRejectsNonHTML(t *testing.T) {
	// A placeholder that contains no markup is the "wrote a stub, called it
	// done" case the smoke check exists to catch.
	dir, name := writeHTML(t, "TODO: build the app\n")

	res, _ := BrowserSmoke(context.Background(), name, SmokeOptions{WorkDir: dir, DisableBrowser: true})
	if res.OK {
		t.Fatalf("expected failure for non-HTML content")
	}
	if len(res.Problems) == 0 || !strings.Contains(res.Problems[0], "HTML structure") {
		t.Fatalf("expected a no-structure problem, got %v", res.Problems)
	}
}

func TestBrowserSmokeMissingFileErrors(t *testing.T) {
	res, err := BrowserSmoke(context.Background(), "nope.html", SmokeOptions{WorkDir: t.TempDir(), DisableBrowser: true})
	if err == nil {
		t.Fatalf("expected error reading a missing file")
	}
	// A setup error still names the path so the caller can report it.
	if res.Path != "nope.html" {
		t.Errorf("result should echo the path, got %q", res.Path)
	}
}

func TestBrowserErrorLinesFiltersNoise(t *testing.T) {
	stderr := strings.Join([]string{
		"[0610/120000.123:INFO:something] starting up",
		"[0610/120000.456:WARNING:foo] a warning, not a failure",
		"[0610/120000.789:ERROR:console] Uncaught ReferenceError: run is not defined",
		"[0610/120000.789:ERROR:console] Uncaught ReferenceError: run is not defined", // dup
	}, "\n")
	got := browserErrorLines(stderr)
	if len(got) != 1 {
		t.Fatalf("expected 1 distinct error line, got %d: %v", len(got), got)
	}
	if !strings.Contains(got[0], "ReferenceError") {
		t.Errorf("error line lost its content: %q", got[0])
	}
}

// TestBrowserSmokeRealBrowser exercises the non-disabled browser path against a
// real installed browser, so the strongest-first load does not silently rot into
// dead code that always falls back to static. It is skipped when no headless
// browser resolves on the machine; when one does, it asserts that a healthy page
// loads via the browser method and that a page with a genuine uncaught runtime
// error is reported as a failure (not passed) by that same path.
func TestBrowserSmokeRealBrowser(t *testing.T) {
	bin := resolveHeadlessBrowser("")
	if bin == "" {
		t.Skip("no headless browser available")
	}

	t.Run("healthy page loads via browser", func(t *testing.T) {
		const page = `<!doctype html><html><body>
<button id="go">Go</button>
<script>document.getElementById('go').addEventListener('click', function(){ console.log('ok'); });</script>
</body></html>`
		dir, name := writeHTML(t, page)

		res, err := BrowserSmoke(context.Background(), name, SmokeOptions{WorkDir: dir})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Method != SmokeMethodBrowser {
			t.Fatalf("expected browser method, got %q (browser path not exercised)", res.Method)
		}
		if !res.OK {
			t.Fatalf("expected healthy page to pass, got problems: %v", res.Problems)
		}
	})

	t.Run("runtime error fails via browser", func(t *testing.T) {
		// nonexistentFunction() is an uncaught ReferenceError at load: only a real
		// browser load catches it, and it must come back as a failure, not a pass.
		const page = `<!doctype html><html><body>
<div id="app"></div>
<script>nonexistentFunction();</script>
</body></html>`
		dir, name := writeHTML(t, page)

		res, err := BrowserSmoke(context.Background(), name, SmokeOptions{WorkDir: dir})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Method != SmokeMethodBrowser {
			t.Fatalf("expected browser method, got %q (browser path not exercised)", res.Method)
		}
		if res.OK {
			t.Fatalf("expected uncaught runtime error to fail; got pass with controls %v", res.Controls)
		}
	})
}

func TestResolveHeadlessBrowserUnknownPreferred(t *testing.T) {
	// An explicit preferred binary that does not resolve yields "", not a probe
	// of the PATH defaults — the caller asked for a specific browser.
	if got := resolveHeadlessBrowser("definitely-not-a-real-browser-xyz"); got != "" {
		t.Errorf("expected empty path for unknown browser, got %q", got)
	}
}
