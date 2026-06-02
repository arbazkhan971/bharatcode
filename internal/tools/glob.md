When to call this tool

Use `glob` when BharatCode needs to discover files by name or extension before
reading, editing, or searching them. It supports ordinary glob characters and
`**` for recursive workspace matches.

Arguments:

- `pattern` string, required: glob pattern such as `**/*.go` or `internal/*/*.md`.
- `path` string, optional: workspace-relative directory to search from. Defaults to the workspace root.

What success looks like:

The result is a lexicographically sorted list of workspace-relative file paths,
one per line. Paths use forward slashes so the model sees the same shape across
operating systems.

Failure cases:

Malformed JSON, a missing pattern, an invalid path, or a path that escapes the
BharatCode workspace returns an error result. If the pattern is valid but no
files match, the tool reports that directly.
