When to call this tool

Use `diagnostics` when BharatCode needs language-server errors, warnings, or
hints for a file before or after changing code. It is the fastest way to ask the
workspace tooling what is currently broken.

Arguments:

- `path` string, optional: workspace-relative file to inspect. Omit it to scan
  supported source files in the workspace.
- `severity` string, optional: minimum severity to report — one of `error`,
  `warning`, `info`, or `hint`. Only diagnostics at that level or more severe are
  returned (`error` is most severe). Use `error` to focus on what blocks a build
  when a workspace scan is noisy with hints. Omit it to report every severity.

What success looks like:

The result opens with a one-line summary tallying the total, the files touched,
and the per-severity counts, e.g. `3 diagnostics across 2 files (2 errors, 1
warning):`. Beneath it is a sorted list of diagnostics formatted as
`path:line:column: severity: message`. When the language server supplies them, a
rule code follows in brackets and the reporting source in parentheses, e.g.
`main.rs:3:9: error: cannot find value `x` [E0425] (rustc)`. When the offending
source line can be read it is shown, trimmed, indented beneath the message so you
see the code at fault without a separate view. When the language server links
other locations to a diagnostic (the conflicting prior declaration, an unused
import's use site), each is listed indented as `related: path:line:column:
message` so you can act on the cross-reference directly.

Failure cases:

Malformed JSON, an unavailable LSP manager, a path outside the BharatCode
workspace, or an infrastructure failure while asking the language server returns
an error. If no diagnostics exist, the tool says so directly.
