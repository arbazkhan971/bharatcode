Call `edit` when BharatCode needs to make one exact text replacement in an existing workspace file.

## When to call this tool

Use this after reading enough surrounding context to know the exact old text. Prefer it for focused changes where a unique snippet can identify the target location.

## Arguments

- `path` string, required: workspace-relative or absolute path to edit.
- `old_string` string, required: exact text currently present in the file.
- `new_string` string, required: replacement text to write.
- `replace_all` boolean, optional: replace every match instead of requiring a unique match.

## Success

The file is rewritten atomically, BharatCode records the before and after file hashes, and the result reports how many replacements were applied.

## Failures

The tool fails when the path escapes the workspace, arguments are malformed, the old text is absent, the old text appears more than once without `replace_all`, permission is denied, or the write cannot be recorded.
