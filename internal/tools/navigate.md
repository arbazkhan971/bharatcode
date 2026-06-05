When to call this tool

Use `navigate` to follow code the way an IDE does, using the language server.
Given a symbol's position (file, line, and column), it answers three questions:

- `definition`: where is this symbol declared? Jump from a call site to the
  function/type/variable it resolves to. More precise than grepping a name.
- `references`: where is this symbol used? Lists every use site across the
  workspace, including the declaration. Use this before renaming or changing a
  signature to gauge blast radius.
- `hover`: what is this symbol? Returns the language server's type, signature,
  and documentation for it.

Position comes from other tools: the `symbols`, `grep`, and `view` tools all
report 1-based `path:line:column`, which you pass straight in here.

Arguments:

- `path` string, required: workspace-relative file containing the symbol.
- `line` integer, required: 1-based line of the symbol.
- `column` integer, optional: 1-based column of the symbol on that line.
  Defaults to 1 (start of line); point it at the symbol's name for accuracy.
- `action` string, optional: `definition` (default), `references`, or `hover`.

What success looks like:

For `definition` and `references`, a sorted list of
`path:line:column: <source line>` entries, workspace-relative where possible;
the trailing source line is the trimmed code at that site (omitted when the
file or line cannot be read). For `hover`, the language server's text.

Failure cases:

Malformed JSON, an unavailable LSP manager, a path outside the BharatCode
workspace, a missing path, a line below 1, an unknown action, or an
infrastructure failure while asking the language server returns an error. When
the server has no answer (undefined symbol, no references, no hover), the tool
says so directly.
