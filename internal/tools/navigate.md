When to call this tool

Use `navigate` to follow code the way an IDE does, using the language server.
Given a symbol's position (file, line, and column), it answers several
questions:

- `definition`: where is this symbol defined? Jump from a call site to the
  function/type/variable it resolves to. More precise than grepping a name.
- `declaration`: where is this symbol declared? In languages that separate the
  declaration from the definition (a C/C++ header vs its source file, a
  TypeScript ambient `declare`) this lands on the declaration site rather than
  the implementation `definition` jumps to. For languages that do not separate
  them, the server returns the same location as `definition`.
- `type_definition`: where is the *type* of this symbol declared? From a
  variable or expression, jump to the declaration of its type rather than its
  own definition.
- `implementation`: what concretely implements this? From an interface or
  abstract method, lists the concrete types/methods that satisfy it.
- `references`: where is this symbol used? Lists every use site across the
  workspace, including the declaration. Use this before renaming or changing a
  signature to gauge blast radius. Pass `include_declaration: false` to list
  only the use sites, leaving the declaration out.
- `incoming_calls`: which functions call this one? Lists the callers from the
  language server's call hierarchy. More precise than `references` for a
  function: it reports only call sites, not every textual mention.
- `outgoing_calls`: which functions does this one call? Lists the callees, so
  you can map a function's behavior without reading its whole body.
- `hover`: what is this symbol? Returns the language server's type, signature,
  and documentation for it.
- `signature`: what does this call expect? Point at a call's arguments to get
  the function's signature(s), with the argument the cursor is currently on
  marked, plus parameter documentation when the server provides it.

Position comes from other tools: the `symbols`, `grep`, and `view` tools all
report 1-based `path:line:column`, which you pass straight in here.

Arguments:

- `path` string, required: workspace-relative file containing the symbol.
- `line` integer, required: 1-based line of the symbol.
- `column` integer, optional: 1-based column of the symbol on that line.
  Defaults to 1 (start of line); point it at the symbol's name for accuracy.
- `action` string, optional: `definition` (default), `declaration`,
  `type_definition`, `implementation`, `references`, `incoming_calls`,
  `outgoing_calls`, `hover`, or `signature`.
- `include_declaration` boolean, optional: only meaningful for `references`.
  Defaults to `true` (the declaration is listed among the references); set it to
  `false` to list only the use sites.

What success looks like:

For `definition`, `declaration`, `type_definition`, `implementation`, `references`,
`incoming_calls`, and `outgoing_calls`, a sorted list of
`path:line:column: <source line>` entries, workspace-relative where possible;
the trailing source line is the trimmed code at that site (omitted when the
file or line cannot be read). `references`, `incoming_calls`, and
`outgoing_calls` additionally lead with a summary line (`N references across M
files:`, `N callers across M files:`, `N callees across M files:`) so you can
gauge a symbol's blast radius or call-hierarchy fan-out at a glance. For
`hover` and `signature`, the language server's text, with `signature` marking
the active overload (`→`) and the parameter the cursor is on. A very long hover
or signature is capped on a line boundary with a `... [N more lines truncated]`
notice; re-read the source directly if you need the rest.

Failure cases:

Malformed JSON, an unavailable LSP manager, a path outside the BharatCode
workspace, a missing path, a line below 1, an unknown action, or an
infrastructure failure while asking the language server returns an error. When
the server has no answer (undefined symbol, no references, no hover, no
signature), the tool says so directly.
