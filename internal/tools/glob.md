When to call this tool

Use `glob` when BharatCode needs to discover files by name or extension before
reading, editing, or searching them. It supports ordinary glob characters,
`**` for recursive workspace matches, and brace alternation such as
`**/*.{ts,tsx}` to match any of several extensions in one pattern.

Arguments:

- `pattern` string, required: glob pattern such as `**/*.go` or `internal/*/*.md`.
- `path` string, optional: workspace-relative directory to search from. Defaults to the workspace root.
- `limit` integer, optional: maximum number of paths to return (newest-first). Defaults to a built-in cap; larger values are clamped to it.

What success looks like:

The result is a list of workspace-relative file paths, one per line, ordered by
modification time with the most recently changed files first (paths sharing a
timestamp fall back to lexicographic order). Output is bounded: when more files
match than the limit (or the built-in cap), only the newest are listed and a
trailing `[results capped: showing first N of M files]` notice records the total
so you know to narrow the pattern or pass a path. The files you most likely care
about — the ones just edited — surface at the top. Paths use forward slashes so
the model sees the same shape across operating systems. Paths matched by a
`.gitignore` (the workspace root's and any in subdirectories, e.g.
`build/.gitignore`) or the `.git` directory are skipped, so vendored and build
output such as `node_modules` or `dist` never appears — the same nested filtering
`ls` and `grep` apply.

Failure cases:

Malformed JSON, a missing pattern, an invalid path, or a path that escapes the
BharatCode workspace returns an error result. If the pattern is valid but no
files match, the tool reports that directly.
