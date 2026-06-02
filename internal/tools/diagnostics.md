When to call this tool

Use `diagnostics` when BharatCode needs language-server errors, warnings, or
hints for a file before or after changing code. It is the fastest way to ask the
workspace tooling what is currently broken.

Arguments:

- `path` string, optional: workspace-relative file to inspect. Omit it to scan
  supported source files in the workspace.

What success looks like:

The result is a sorted list of diagnostics formatted as
`path:line:column: severity: message`. Source names are included when the
language server provides them.

Failure cases:

Malformed JSON, an unavailable LSP manager, a path outside the BharatCode
workspace, or an infrastructure failure while asking the language server returns
an error. If no diagnostics exist, the tool says so directly.
