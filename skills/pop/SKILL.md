---
name: pop
description: Pop the top topic from the current Claude Code session's stack. Invoke when finishing the sub-task currently on top — the next-down topic (or empty) becomes the new focus.
---

Run the following command via the Bash tool, then relay its stdout verbatim to the user.

```
stack pop
```

The topic you just popped is no longer the active focus. If the stack still has items, the new top is what you should return to. If empty, ask the user what's next.

If the command fails because the stack is empty, tell the user "(stack is empty)" and stop — don't infer that something must be popped.

## When the agent should self-invoke

When you finish a sub-task that sits on top of the stack, pop it without waiting for the user to type `/pop`. Pair the pop with the moment you deliver the result — "task done, here's the outcome, popping" — rather than deferring to end-of-turn or until the user prompts. A stale top in the TUI is a worse signal than a momentary empty stack.

Pop when:

- The sub-task that defines the current top is *complete* — committed, validated, or explicitly handed off.

Don't pop just because:

- You finished one tool call within a larger task. The task isn't done.
- The conversation drifted briefly. Drifts come back.
- You're about to push something new. Push goes on top; the old top stays underneath.
