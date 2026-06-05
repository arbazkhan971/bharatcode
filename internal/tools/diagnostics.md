When to call this tool

Use `diagnostics` when BharatCode needs language-server errors, warnings, or
hints for a file before or after changing code. It is the fastest way to ask the
workspace tooling what is currently broken.

Arguments:

- `path` string, optional: workspace-relative file to inspect. Omit it to scan
  supported source files in the workspace.

What success looks like:

The result opens with a one-line summary tallying the total, the files touched,
and the per-severity counts, e.g. `3 diagnostics across 2 files (2 errors, 1
warning):`. Beneath it is a sorted list of diagnostics formatted as
`path:line:column: severity: message`. When the language server supplies them, a
rule code follows in brackets and the reporting source in parentheses, e.g.
`main.rs:3:9: error: cannot find value `x` [E0425] (rustc)`.

Failure cases:

Malformed JSON, an unavailable LSP manager, a path outside the BharatCode
workspace, or an infrastructure failure while asking the language server returns
an error. If no diagnostics exist, the tool says so directly.
