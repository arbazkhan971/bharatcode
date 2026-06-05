Call `multiedit` when BharatCode needs to apply several exact text replacements to one workspace file as a single operation.

## Rules

- **You MUST call `view` on a file before calling `multiedit` on it.** The tool is rejected if the file changed on disk since the last read.
- Edits are applied in order to an in-memory copy; a failure at any step leaves the on-disk file unchanged.
- Each `old` string must uniquely identify its target. Widen the anchor with surrounding lines rather than using `replace_all` for a single occurrence.

## Arguments

- `path` string, required: workspace-relative or absolute path to edit.
- `edits` array, required: ordered replacement steps.
- `edits[].old` string, required: exact text to find at that step, including all whitespace and newlines.
- `edits[].new` string, required: replacement text for that step.
- `edits[].replace_all` boolean, optional: replace every match for that step (use sparingly).

## Matching

Each `old` is matched exactly first. If an exact match is not found and `old` spans two or more lines, BharatCode falls back to the same whitespace-tolerant matching used by `edit` (line-trimmed, internal-whitespace-normalized, then block anchors) so indentation drift does not defeat a correct edit; flexible matches must still be unambiguous, and the result notes how many edits used the fallback. `new` is written verbatim, so include the file's indentation in it.

## Success

BharatCode rewrites the file once, records the write in the file tracker, and returns replacement counts plus before and after hashes in metadata along with a compact unified diff of the changed lines so you can see exactly what changed. When a language server is configured for the file, any errors and warnings it reports for the edited file are appended to the result — fix the errors before moving on.

## Failures

The tool fails before touching disk when:
- The file has not been read in this session — `view` it first so the edits target its current contents.
- The file was modified on disk since it was last read (re-view and try again).
- Any edit's `old` is not found — the error includes a near-match hint for whitespace/indentation mismatches.
- Any edit's `old` is non-unique without `replace_all` — the count is reported so you can widen the anchor.
- Arguments are malformed, path is outside the workspace, permission is denied, or the write cannot be recorded.
