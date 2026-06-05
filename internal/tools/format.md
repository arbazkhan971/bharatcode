Reformat a source file in place using its language server's formatter (e.g. gofmt via gopls, prettier-equivalent servers, rustfmt via rust-analyzer).

Pass the workspace-relative `path` of a single file. The language server computes the formatting edits and they are applied and written back to disk atomically. The tool reports how many edits were applied, or that the file was already formatted.

Use this after writing or editing a file to normalize indentation, spacing, and import order, instead of shelling out to a formatter. Requires a language server to be configured and available for the file's language; otherwise it reports that formatting is unavailable.
