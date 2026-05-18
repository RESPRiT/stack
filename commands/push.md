---
description: Push a topic onto the current session's stack
argument-hint: <topic>
---

Run the following command via the Bash tool, then relay its stdout verbatim to the user.

```
stack push "$ARGUMENTS"
```

Then update your own working model: the topic you just pushed is now the active focus. When you finish it, call `/pop`. If the user asks "what are you working on?", read the top of the stack rather than guessing.

If the command fails (no `stack` binary on PATH, etc.), tell the user what went wrong and stop.
