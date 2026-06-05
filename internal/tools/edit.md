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

## Matching

`old_string` is matched exactly first. If an exact match is not found and `old_string` spans two or more lines, BharatCode falls back to whitespace-tolerant matching (line-trimmed, then internal-whitespace-normalized, then first/last-line block anchors for blocks of three or more lines) so indentation drift between your text and the file does not defeat an otherwise-correct edit. A flexible match still has to be unambiguous — a block that maps to more than one location is rejected like a duplicate exact match — and the result notes which strategy matched. Single-line mismatches stay strict so you get a whitespace hint instead. `new_string` is always written verbatim, so include the file's indentation in it.

## Success

The file is rewritten atomically, BharatCode records the before and after file hashes, and the result reports how many replacements were applied along with a compact unified diff of the changed lines so you can see exactly what changed. When a language server is configured for the file, the result also lists any errors and warnings it reports for the edited file — fix the errors before moving on.

## Failures

The tool fails when:
- The path escapes the workspace or arguments are malformed.
- The file has not been read in this session — `view` it first so the edit targets its current contents.
- The file was modified on disk since it was last read (re-view the file and try again).
- `old_string` is not found — the error includes a near-match hint when whitespace or a close substring is detected, so you can correct the text on the next attempt.
- `old_string` appears more than once without `replace_all` — the error reports the count; widen the anchor rather than using `replace_all`.
- Permission is denied or the write cannot be recorded.
