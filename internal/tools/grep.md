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

Smart-case matching:

When the pattern is entirely lowercase the search is case-insensitive, so
`myfunction` matches `MyFunction`, `MYFUNCTION`, and `myfunction` alike. When
the pattern contains any uppercase letter the search is case-sensitive and
exact. This mirrors ripgrep's `--smart-case` behaviour and applies on both
the rg path and the Go fallback.

Match cap:

Results are capped at 1000 matching lines (content mode) or 1000 matching
files (files_with_matches / count mode). When the cap is reached the output
ends with a `[results capped: showing first N matches]` notice. To stay under
the cap, narrow with `include`, scope `path` to a subtree, or switch
`output_mode` to `count` or `files_with_matches` before requesting full
content.

Binary and ignored files:

Binary files (those containing NUL bytes) are never included in results. The
following directories are always skipped: `.git`, `node_modules`, `vendor`,
`dist`, `.svn`, `.hg`. Additionally, plain directory names in a `.gitignore`
at the workspace root (e.g. `build/`) are skipped by the Go fallback. These
exclusions apply regardless of whether ripgrep is installed, so results are
consistent on any machine.

Failure cases:

Malformed JSON, a missing pattern, invalid regex syntax, or a path outside the
BharatCode workspace returns an error result. If no files match, the output says
that no matches were found.
