When to call this tool

Use `symbols` to locate where a function, type, method, constant, or variable is
defined, using the language server's index. It is faster and more precise than
grepping for a name, because it returns the declaration site (not every textual
mention) with the symbol's kind.

Two modes:

- Workspace search: set `query` to a symbol name to find matching declarations
  across the whole workspace. Use this to answer "where is X defined?".
- File outline: set `path` to a workspace-relative file to list the symbols
  declared in it. Add `query` to filter that outline by a case-insensitive
  substring; omit `query` to see the full outline.

Arguments:

- `query` string: symbol name to search for. Required (non-empty) when `path`
  is omitted; optional as a filter when `path` is set.
- `path` string, optional: workspace-relative file to outline instead of
  searching the workspace.
- `kind` string, optional: comma-separated kind labels to restrict results to,
  e.g. `function`, `method`, `class`, `struct`, `interface`, `variable`,
  `constant`, `enum`. Use the same labels shown in the output. Omit to list every
  kind.

What success looks like:

The result is a sorted list formatted as `path:line:column: kind name`. In a file
outline the symbol's signature or type is appended when the language server
supplies one (e.g. `function Add func(a int, b int) int`). A full file outline is
rendered as an indented tree: nested symbols (a struct's fields, a class's
methods) are indented two spaces per level beneath their container. A workspace
search or a filtered outline stays flat and appends the enclosing container as
`(in container)` when present. A very broad query can match more symbols than fit
usefully in one result; the list is capped and a trailing `... and N more (M
total) not shown` line reports what was elided, so narrow the `query` (or add a
`kind` filter) to see the rest.

Failure cases:

Malformed JSON, an unavailable LSP manager, a path outside the BharatCode
workspace, a missing query in workspace mode, or an infrastructure failure while
asking the language server returns an error. If no symbols match, the tool says
so directly.
