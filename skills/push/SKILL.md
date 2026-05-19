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

## When the agent should self-invoke

Don't wait for the user to type `/push`. When you start a discrete sub-task whose state would be useful to track — kicking off a multi-step refactor, beginning a debugging investigation, taking on a side task — push it yourself via the Skill tool. The TUI shows the user what you're working on; an empty stack while you're deep in a sub-task is a missed signal.

Constraints:

- **Only push what justifies a line in the TUI.** Trivial tool calls (a single grep, one quick read, a short clarifying question) don't merit a push. Multi-step or multi-turn work does.
- **Don't push and pop within the same response.** If a task is small enough to finish in the turn it started, skip the stack entirely — pushing and popping it just adds noise.
- **Don't auto-push on every user message.** Push when you're starting work, not when the user is just talking.
