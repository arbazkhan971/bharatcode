Call `multiedit` when BharatCode needs to apply several exact text replacements to one workspace file as a single operation.

## When to call this tool

Use this for coordinated edits where each replacement depends on the previous one. All edits are checked against an in-memory copy first, so a later failed match leaves the file unchanged.

## Arguments

- `path` string, required: workspace-relative or absolute path to edit.
- `edits` array, required: ordered replacement steps.
- `edits[].old` string, required: exact text to find at that step.
- `edits[].new` string, required: replacement text for that step.
- `edits[].replace_all` boolean, optional: replace every match for that step.

## Success

BharatCode rewrites the file once, records the write in the file tracker, and returns replacement counts plus before and after hashes in metadata.

## Failures

The tool fails before touching disk when any edit is malformed, missing, non-unique without `replace_all`, outside the workspace, denied by permission, or unable to be recorded.
