Call `write` when BharatCode needs to create a workspace file or replace the full contents of a file that was already viewed in this session.

## When to call this tool

Use this for new files, generated fixtures, or full-file rewrites. For an existing file, call `view` first so BharatCode has a current read baseline and can avoid overwriting unseen user work.

## Arguments

- `path` string, required: workspace-relative or absolute destination path.
- `content` string, required: complete file contents to write.

## Success

The parent directory is created when needed, the file is written atomically, and the file tracker records a create or edit with before and after hashes.

## Failures

The tool fails when the path escapes the workspace, permission is denied, an existing file has not been viewed in this session, the parent cannot be created, or the write cannot be recorded.
