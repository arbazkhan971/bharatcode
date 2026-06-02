When to call this tool

Use `todo` when BharatCode needs to maintain a short in-session checklist for a
multi-step coding task. It is for planning and progress tracking during the
current session, not for editing project files.

Arguments:

- `action` string, required: one of `add`, `update`, `delete`, or `list`.
- `items` array, optional: todo objects with `id`, `content` or `text`, and `status`.
- `item` object, optional: a single todo object when an array would be noisy.
- `id`, `text`, `status` strings, optional: shorthand fields for single-item updates.

What success looks like:

The result lists the changed items or the current todo list. New items get
stable numeric ids. Status values are normalized to `pending`, `in_progress`,
or `done` when possible.

Failure cases:

Malformed JSON, an unknown action, missing item content, or an update/delete for
an unknown id returns an error result. Todo state is scoped to the BharatCode
session bus supplied to the tool.
