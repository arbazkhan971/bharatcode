When to call this tool

Use `grep` when BharatCode needs to find text or a regular expression inside
workspace files. It is best for locating symbols, TODO comments, configuration
keys, error messages, or call sites before making a code change.

Arguments:

- `pattern` string, required: regular expression to search for.
- `path` string, optional: workspace-relative file or directory. Defaults to the workspace root.
- `include` string, optional: file-name glob such as `*.go` to narrow the search.
- `output_mode` string, optional: `content`, `files_with_matches`, or `count`.

What success looks like:

The result is a stable text list. Content mode returns `path:line:content`;
file mode returns one matching path per line; count mode returns `path:count`.

Truncation: grep does not cap its own output, so a broad pattern can return a
very large list that later gets truncated by the surrounding context budget. To
keep results small and useful, narrow with `include`, scope `path` to a
subtree, or switch `output_mode` to `count` or `files_with_matches` before
falling back to full `content`.

Failure cases:

Malformed JSON, a missing pattern, invalid regex syntax, or a path outside the
BharatCode workspace returns an error result. If no files match, the output says
that no matches were found.
