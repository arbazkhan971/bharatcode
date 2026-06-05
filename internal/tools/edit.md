Call `edit` when BharatCode needs to make one exact text replacement in an existing workspace file.

## Rules

- **You MUST call `view` on a file before calling `edit` on it.** Editing without a prior read risks matching stale content and will be rejected if the file changed since the last read.
- Include enough surrounding lines in `old_string` to make it unique. Do not flip `replace_all` just to avoid uniqueness errors — prefer widening the anchor instead.
- For multiple edits to the same file in one step, prefer `multiedit` over repeated `edit` calls.

## Arguments

- `path` string, required: workspace-relative or absolute path to edit.
- `old_string` string, required: exact text currently present in the file, including all whitespace and newlines.
- `new_string` string, required: replacement text to write.
- `replace_all` boolean, optional: replace every match instead of requiring a unique match (use sparingly).

## Success

The file is rewritten atomically, BharatCode records the before and after file hashes, and the result reports how many replacements were applied.

## Failures

The tool fails when:
- The path escapes the workspace or arguments are malformed.
- The file was modified on disk since it was last read (re-view the file and try again).
- `old_string` is not found — the error includes a near-match hint when whitespace or a close substring is detected, so you can correct the text on the next attempt.
- `old_string` appears more than once without `replace_all` — the error reports the count; widen the anchor rather than using `replace_all`.
- Permission is denied or the write cannot be recorded.
