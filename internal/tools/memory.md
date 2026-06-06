Persist and retrieve notes across sessions. Use this tool to remember facts about the user, project conventions, decisions, preferences, and anything else that should survive between conversations.

**When to use:**
- User states a preference ("I prefer tabs over spaces") → write it
- You discover a project convention ("uses pytest, not unittest") → write it
- You need to recall something from a prior session → list or read
- A remembered fact is no longer correct → delete it and write the updated version

**Scope:**
- `global` (default): stored in `~/.config/bharatcode/memory.json`; survives across all projects
- `project`: stored in `.bharatcode/memory.json` inside the current working directory; project-specific facts

**Actions:**

`write` — store or overwrite a note:
```json
{"action":"write","key":"user_prefers_tabs","content":"User always uses tabs for indentation, not spaces."}
```

`list` — show all stored keys with short previews:
```json
{"action":"list"}
{"action":"list","scope":"project"}
```

`read` — retrieve a specific note in full:
```json
{"action":"read","key":"test_framework"}
```

`delete` — remove a note that is no longer accurate:
```json
{"action":"delete","key":"old_convention"}
```

**Key naming:** use short, lowercase, descriptive names like `user_prefers_tabs`, `test_framework`, `db_migration_approach`. Avoid spaces.
