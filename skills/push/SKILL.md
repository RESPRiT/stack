---
name: push
description: Push a topic onto the current Claude Code session's stack so the agent (and any live `stack tui` panel) knows what's now the active focus. Invoke when starting a discrete sub-task whose progress should be visible in the stack.
argument-hint: <topic>
---

Run the following command via the Bash tool, then relay its stdout verbatim to the user.

```
stack push "$ARGUMENTS"
```

You may re-phrase `$ARGUMENTS` before passing it to `stack push` when the original is verbose, ambiguous, or imprecise — the TUI shows the topic, so a concise, well-scoped phrasing is more useful than a sprawling one. Preserve the meaning; do not change scope. If the original is already crisp (under ~10 words, unambiguous, action-oriented), pass it through verbatim. When you do re-phrase, briefly note what you changed in your reply so the user can correct you if you drifted.

Then update your own working model: the topic you just pushed is now the active focus. When you finish it, call `/pop`. If the user asks "what are you working on?", read the top of the stack rather than guessing.

If the command fails (no `stack` binary on PATH, etc.), tell the user what went wrong and stop.
