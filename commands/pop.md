---
description: Pop the top topic from the current session's stack
---

Run the following command via the Bash tool, then relay its stdout verbatim to the user.

```
stack pop
```

The topic you just popped is no longer the active focus. If the stack still has items, the new top is what you should return to. If empty, ask the user what's next.

If the command fails because the stack is empty, tell the user "(stack is empty)" and stop — don't infer that something must be popped.
