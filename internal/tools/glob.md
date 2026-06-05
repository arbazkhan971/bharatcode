When to call this tool

Use `glob` when BharatCode needs to discover files by name or extension before
reading, editing, or searching them. It supports ordinary glob characters,
`**` for recursive workspace matches, and brace alternation such as
`**/*.{ts,tsx}` to match any of several extensions in one pattern.

Arguments:

- `pattern` string, required: glob pattern such as `**/*.go` or `internal/*/*.md`.
- `path` string, optional: workspace-relative directory to search from. Defaults to the workspace root.

What success looks like:

The result is a list of workspace-relative file paths, one per line, ordered by
modification time with the most recently changed files first (paths sharing a
timestamp fall back to lexicographic order). The files you most likely care
about — the ones just edited — surface at the top. Paths use forward slashes so
the model sees the same shape across operating systems. Paths matched by the
workspace `.gitignore` (and the `.git` directory) are skipped, so vendored and
build output such as `node_modules` or `dist` never appears — the same filtering
`ls` and `grep` apply.

Failure cases:

Malformed JSON, a missing pattern, an invalid path, or a path that escapes the
BharatCode workspace returns an error result. If the pattern is valid but no
files match, the tool reports that directly.
