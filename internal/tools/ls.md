When to call this tool

Use `ls` when BharatCode needs a quick view of one directory in the workspace.
It is useful before choosing a file to inspect, confirming generated files, or
checking the shape of a module without walking the whole tree.

Arguments:

- `path` string, optional: workspace-relative directory to list. Defaults to the workspace root.
- `ignore` array of strings, optional: extra names or glob patterns to hide from the listing.

What success looks like:

The result is a sorted list of immediate children. Directory names end with `/`.
The tool reads the workspace `.gitignore` and hides ignored directories such as
`node_modules/` by default.

Failure cases:

Malformed JSON, a missing directory, a file path instead of a directory, or a
path outside the BharatCode workspace returns an error result. An empty visible
directory is reported as empty.
