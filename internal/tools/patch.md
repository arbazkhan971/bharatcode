Call `patch` when BharatCode needs to apply a coherent change set spanning **several files at once** — the multi-file complement to `edit` and `multiedit`. Provide a single standard unified diff (the output of `git diff` or `diff -u`) and the whole patch is applied atomically: every file changes, or none does.

## When to use

- Prefer `edit`/`multiedit` for changes to a single file.
- Reach for `patch` when one logical change touches multiple files, or when you already have a unified diff to apply (e.g. created, modified, and deleted files in one step).

## Rules

- **You MUST call `view` on every existing file the patch modifies or deletes before calling `patch`.** A file that changed on disk since its last read is rejected, and the whole patch is refused before any file is written.
- Validation happens first across all files; if any section fails to apply, nothing is written.
- File creation and deletion use `/dev/null`: `--- /dev/null` with `+++ b/path` creates a file; `--- a/path` with `+++ /dev/null` deletes one.

## Arguments

- `patch` string, required: a unified diff. Each file section is a `--- ` header, a `+++ ` header, and one or more `@@ -old,len +new,len @@` hunks whose body lines are prefixed with a space (context), `-` (removed), or `+` (added).

## Matching

Each hunk is located by its declared line number and verified against the file's actual content; if the line drifted, BharatCode searches forward for the exact context block so a slightly stale line number still applies. Git `a/` and `b/` path prefixes and trailing timestamps in headers are handled, and the file's trailing-newline state is preserved.

## Success

BharatCode writes each created or modified file, removes each deleted file, records every change in the file tracker, and returns a per-file summary with a compact unified diff of what changed. When a language server is configured, errors and warnings for each written file are appended — fix the errors before moving on.

## Failures

The patch is refused before touching disk when:
- The patch cannot be parsed, or a hunk's context does not match the file.
- A modified or deleted file does not exist, or a created file already exists.
- A modified or deleted file was not read this session, or changed on disk since its last read.
- A path is outside the workspace, permission is denied, or a write cannot be recorded.
