---
name: push
description: Push a topic onto the current Claude Code session's stack so the agent (and any live `stack tui` panel) knows what's now the active focus. Invoke when starting a discrete sub-task whose progress should be visible in the stack.
argument-hint: <topic>
---

Run the following command via the Bash tool, then relay its stdout verbatim to the user.

```
stack push "$ARGUMENTS"
```

Then update your own working model: the topic you just pushed is now the active focus. When you finish it, call `/pop`. If the user asks "what are you working on?", read the top of the stack rather than guessing.

If the command fails (no `stack` binary on PATH, etc.), tell the user what went wrong and stop.
