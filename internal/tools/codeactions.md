When to call this tool

Use `codeactions` to ask the language server which quick fixes and refactorings
it offers at a position or selection — the same menu an IDE shows under a
lightbulb. Typical results are "organize imports", "remove unused declaration",
"fill struct literal", "extract function", or a fix for a specific diagnostic.

This tool only lists the available actions; it does not apply them. Use it to
discover what is on offer, then make the change yourself with `edit`/`multiedit`,
or call `format` for whole-file reformatting.

Position comes from other tools: `diagnostics`, `symbols`, `grep`, and `view`
all report 1-based `path:line:column`, which you pass straight in here. Point it
at a diagnostic's location to find the matching quick fix.

Arguments:

- `path` string, required: workspace-relative file to inspect.
- `line` integer, required: 1-based line where the action should apply.
- `column` integer, optional: 1-based start column. Defaults to 1.
- `end_line` integer, optional: 1-based end line of the selection. Defaults to
  `line`.
- `end_column` integer, optional: 1-based end column. Defaults to `column`, i.e.
  a cursor position rather than a span. Widen the range to surface refactorings
  that act on a selection.

What success looks like:

A numbered list of actions, each showing its title, kind (e.g.
`quickfix`, `source.organizeImports`, `refactor.extract`) when the server
reports one, and a note of how it would take effect — an inline edit, a
server-side command, or both.

Failure cases:

Malformed JSON, an unavailable LSP manager, a path outside the BharatCode
workspace, a missing path, a line below 1, or an infrastructure failure while
asking the language server returns an error. When the server offers nothing for
the range, the tool says so directly.
