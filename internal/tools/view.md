Call `view` when BharatCode needs to inspect a file in the current workspace before reasoning about it or changing it.

## When to call this tool

Use this for source files, configuration files, documentation, and small image assets. A successful read also records that the path was viewed in the current session, which lets later write operations safely overwrite that same file.

## Arguments

- `path` string, required: workspace-relative or absolute path to read.
- `offset` integer, optional: one-based starting line for text output.
- `limit` integer, optional: maximum number of text lines to return.

## Success

Text files return numbered lines. Supported image files return a short text summary and image data in metadata for the interface.

## Failures

The tool fails when the path is missing, is a directory, is outside the workspace, is invalid UTF-8 text, or is an image larger than BharatCode's safety limit.
