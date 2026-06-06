Reformat a source file in place using its language server's formatter (e.g. gofmt via gopls, prettier-equivalent servers, rustfmt via rust-analyzer).

Pass the workspace-relative `path` of a single file. The language server computes the formatting edits and they are applied and written back to disk atomically. The tool reports how many edits were applied along with a unified diff of the changes, or that the file was already formatted. After applying, it re-checks the file with the language server and appends any errors or warnings it finds.

To reformat only part of a file, pass a 1-based `line` (and optionally `end_line` for a multi-line span); the rest of the file is left untouched. Omit `line` to format the whole document. Range formatting requires the language server to support it; most do.

Pass `preview: true` to see the unified diff the reformatting would produce without writing anything to disk; re-run with `preview` omitted (or false) to apply it.

Use this after writing or editing a file to normalize indentation, spacing, and import order, instead of shelling out to a formatter. Requires a language server to be configured and available for the file's language; otherwise it reports that formatting is unavailable.
