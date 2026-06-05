When to call this tool

Use `grep` when BharatCode needs to find text or a regular expression inside
workspace files. It is best for locating symbols, TODO comments, configuration
keys, error messages, or call sites before making a code change.

Arguments:

- `pattern` string, required: regular expression to search for.
- `path` string, optional: workspace-relative file or directory. Defaults to the workspace root.
- `include` string, optional: file-name glob such as `*.go` to narrow the search.
- `type` string, optional: filter to a language by file type, like `rg --type go` (e.g. `go`, `py`, `js`, `ts`, `rust`, `java`, `c`, `cpp`, `html`, `css`, `json`, `yaml`, `md`). Combine with `include` to narrow further — both filters must pass.
- `output_mode` string, optional: `content`, `files_with_matches`, or `count`.
- `context` integer, optional: number of lines to show before **and** after each match (like `rg -C`). Takes precedence over `before`/`after` when all three are set.
- `before` integer, optional: number of lines to show before each match (like `rg -B`). Ignored when `context` is set.
- `after` integer, optional: number of lines to show after each match (like `rg -A`). Ignored when `context` is set.
- `multiline` boolean, optional: match patterns that span line boundaries (like `rg -U --multiline-dotall`); `.` matches newlines. Context options are ignored in this mode.
- `case_insensitive` boolean, optional: force case-insensitive matching (like `rg -i`), overriding the default smart-case behaviour.
- `only_matching` boolean, optional: print only the matched (non-empty) parts of each line, one match per row (like `rg -o`), instead of the whole line. Content mode only; ignored in `multiline` mode and `context`/`before`/`after` do not apply.

What success looks like:

The result is a stable text list. Content mode returns `path:line:content` for
matching lines and `path-line-content` for context lines (the `-` separator
distinguishes context from matches, mirroring ripgrep `--no-heading` output).
File mode returns one matching path per line; count mode returns `path:count`.
When context windows from adjacent matches overlap they are merged into a single
group. Non-adjacent groups are separated by `--` on its own line, exactly as
ripgrep prints them.

Smart-case matching:

When the pattern is entirely lowercase the search is case-insensitive, so
`myfunction` matches `MyFunction`, `MYFUNCTION`, and `myfunction` alike. When
the pattern contains any uppercase letter the search is case-sensitive and
exact. This mirrors ripgrep's `--smart-case` behaviour and applies on both
the rg path and the Go fallback. Set `case_insensitive: true` to force a
case-insensitive search regardless of the pattern's case (like `rg -i`), which
overrides smart-case — useful to match a mixed-case pattern such as `HTTP`
against `http` and `Http` alike.

Multiline patterns:

Patterns are single-line by default, matching ripgrep's default behaviour:
the regex engine matches within individual lines, so a pattern that spans a
newline will not match. Set `multiline: true` to search each file as one
buffer (like `rg -U --multiline-dotall`), letting a pattern — and `.` — cross
line boundaries. In `content` mode every line a multiline match touches is
printed as `path:line:content`, exactly as ripgrep prints it. The `context`,
`before`, and `after` options are ignored when `multiline` is set, so the
ripgrep and Go-fallback paths stay consistent. Multiline reads whole files
into memory, so prefer `include`/`path` scoping on large trees.

Only-matching output:

Set `only_matching: true` to print just the substring each match covers rather
than the whole line, the analogue of ripgrep's `-o`/`--only-matching`. Output
stays `path:line:content`, but `content` is the matched text alone, and a line
with several matches yields one row per match (all sharing that line number).
Empty matches — which patterns like `x*` can produce at many positions — are
dropped, exactly as ripgrep does. This is a `content`-mode concern: it has no
effect on `files_with_matches` or `count`, is ignored when `multiline` is set,
and supersedes `context`/`before`/`after` (no context rows are emitted). Both
the ripgrep path and the Go fallback render identically. Use it to extract a
clean list of identifiers, versions, or tokens — e.g. `func \w+` to list every
function name without the surrounding signature.

Type filter:

Set `type` to restrict the search to one language by file type, the analogue of
ripgrep's `--type go`. The mapping is a curated, machine-independent table of the
most common languages (e.g. `py` covers `.py`/`.pyi`/`.pyw`; `ts` covers
`.ts`/`.tsx`/`.mts`/`.cts`; `cpp` covers the usual C++ header/source extensions).
Both the ripgrep path and the Go fallback derive their filter from this single
table — the ripgrep path through a synthetic `--type-add` definition rather than
rg's own larger built-in type set — so a `type` query selects exactly the same
files whether or not ripgrep is installed. `type` and `include` combine with AND
semantics: a file must match both. An unknown type name returns an error listing
the supported names.

Match cap:

Results are capped at 1000 matching lines (content mode) or 1000 matching
files (files_with_matches / count mode). When the cap is reached the output
ends with a `[results capped: showing first N matches]` notice. To stay under
the cap, narrow with `include`, scope `path` to a subtree, or switch
`output_mode` to `count` or `files_with_matches` before requesting full
content.

Binary and ignored files:

Binary files (those containing NUL bytes) are never included in results. The
following directories are always skipped: `.git`, `node_modules`, `vendor`,
`dist`, `.svn`, `.hg`. Additionally, plain directory names in a `.gitignore`
at the workspace root (e.g. `build/`) are skipped by the Go fallback. These
exclusions apply regardless of whether ripgrep is installed, so results are
consistent on any machine.

Gitignore support level:

The Go fallback honours `.gitignore` at the workspace root only. It recognises
plain directory-name entries and patterns ending in `/` (e.g. `dist/`,
`node_modules`). Glob patterns (`*.log`), negation rules (`!keep`), and
`.gitignore` files nested inside subdirectories are not evaluated by the
fallback — they are honoured in full when ripgrep (`rg`) is installed.
Do not rely on nested `.gitignore` exclusions when `rg` is absent.

Failure cases:

Malformed JSON, a missing pattern, invalid regex syntax, or a path outside the
BharatCode workspace returns an error result. If no files match, the output says
that no matches were found.
