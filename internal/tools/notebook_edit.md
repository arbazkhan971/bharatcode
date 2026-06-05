Edit a Jupyter notebook (`.ipynb`) by replacing, inserting, or deleting a whole
cell, while preserving the rest of the notebook (its metadata, format version,
and every other cell's source and outputs).

Use this instead of the `edit`/`write` tools for notebooks: a cell's source is
stored as a JSON array of line strings inside a larger document, so a textual
find/replace easily corrupts the file.

View the notebook first — cells are addressed by the same 1-based `cell_number`
the `view` tool prints (`[Cell N · type]`).

## Arguments

- `path` (required): workspace-relative path to a `.ipynb` notebook.
- `edit_mode`: `replace` (default), `insert`, or `delete`.
- `cell_number`: 1-based cell index.
  - `replace`/`delete`: required — the cell to change or remove.
  - `insert`: the position the new cell takes (existing cells shift down). Omit
    to append at the end.
- `new_source`: the new cell source (used by `replace` and `insert`).
- `cell_type`: `code` or `markdown`.
  - `insert`: required.
  - `replace`: optional — converts the cell to that type. Converting to/within a
    code cell clears its outputs, since they no longer match the new source.

## Notes

- Replacing a code cell's source clears that cell's outputs and resets its
  execution count; re-run the cell to regenerate them.
- The notebook must have been viewed this session and unchanged on disk since,
  so the cell numbers still line up.
