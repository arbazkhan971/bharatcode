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

What success looks like:

The result is a sorted list formatted as `path:line:column: kind name`, with the
enclosing container appended as `(in container)` when the language server
provides one.

Failure cases:

Malformed JSON, an unavailable LSP manager, a path outside the BharatCode
workspace, a missing query in workspace mode, or an infrastructure failure while
asking the language server returns an error. If no symbols match, the tool says
so directly.
