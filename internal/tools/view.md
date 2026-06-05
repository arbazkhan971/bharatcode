Call `view` when BharatCode needs to inspect a file in the current workspace before reasoning about it or changing it.

## When to call this tool

Use this for source files, configuration files, documentation, and small image assets. A successful read also records that the path was viewed in the current session, which lets later write operations safely overwrite that same file.

## Arguments

- `path` string, required: workspace-relative or absolute path to read.
- `offset` integer, optional: one-based starting line for text output.
- `limit` integer, optional: maximum number of text lines to return.

## Success

Text files return numbered lines. Supported image files return a short text summary and image data in metadata for the interface. Jupyter notebooks (`.ipynb`) are rendered as a compact, numbered transcript of their cells — each cell's type, execution count, source, and outputs (stream text, results, and errors, with ANSI codes stripped) — instead of raw JSON.

## Truncation

Individual lines longer than 2000 characters are truncated in place, ending with a marker such as `… [N characters truncated]`, so a minified or single-line file stays viewable.

Text output is capped at roughly 32 KB (configurable). When a file exceeds that budget the result is cut on a line boundary and ends with a marker such as `[Showing lines X-Y of Z. Use offset=N to continue.]`; pass that `offset` (with an optional `limit`) to page through the rest of the file. If a single line is itself larger than the budget, the marker instead suggests a `bash` fallback to stream just that line's bytes. Prefer `offset` and `limit` up front when you only need a known region.

## Failures

The tool fails when the path is missing, is a directory, is outside the workspace, is invalid UTF-8 text, or is an image larger than BharatCode's safety limit.
