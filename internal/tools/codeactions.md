When to call this tool

Use `codeactions` to ask the language server which quick fixes and refactorings
it offers at a position or selection — the same menu an IDE shows under a
lightbulb. Typical results are "organize imports", "remove unused declaration",
"fill struct literal", "extract function", or a fix for a specific diagnostic.

By default this tool lists the available actions. To apply one, call it again
with `apply` set to the action's 1-based number from the listing — useful for
"organize imports" or a specific quick fix. Only edit-based actions can be
applied; server-side commands cannot, and the tool says so. For changes the
server does not offer, make the edit yourself with `edit`/`multiedit`, or call
`format` for whole-file reformatting.

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
- `apply` integer, optional: 1-based index of an action from a prior listing to
  apply. Omit to list rather than apply.

What success looks like:

When listing, a numbered list of actions, each showing its title, kind (e.g.
`quickfix`, `source.organizeImports`, `refactor.extract`) when the server
reports one, and a note of how it would take effect — an inline edit, a
server-side command, or both.

When applying, a summary line plus a unified diff per file the action changed.

Failure cases:

Malformed JSON, an unavailable LSP manager, a path outside the BharatCode
workspace, a missing path, a line below 1, or an infrastructure failure while
asking the language server returns an error. When the server offers nothing for
the range, the tool says so directly.
