Think through a problem step-by-step before acting.

Use `think` to reason explicitly before taking a complex action. Write your
analysis — what you know, what you're uncertain about, the trade-offs between
options, or the sequence of steps you're planning — then read the result back
before proceeding.

**When to use:**
- Before editing multiple files that depend on each other, to plan the exact
  sequence of changes and verify the design holds together.
- When you need to weigh alternatives or check assumptions against code you
  have just read before committing to an approach.
- Any time you catch yourself about to make a change that could be hard to
  reverse — think first, act second.
- To track intermediate conclusions across a long investigation so context is
  not lost between tool calls.

The thought is returned verbatim so your reasoning remains visible in context
for subsequent turns. `think` has no side effects: it reads nothing, writes
nothing, and does not require user approval.
