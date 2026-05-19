---
name: push
description: Push a topic onto the current Claude Code session's stack so the agent (and any live `stack tui` panel) knows what's now the active focus. Invoke when starting a discrete sub-task whose progress should be visible in the stack.
argument-hint: <topic>
---

Run the following command via the Bash tool, then relay its stdout verbatim to the user.

```
stack push "$ARGUMENTS"
```

You may re-phrase `$ARGUMENTS` before passing it to `stack push` when the original is verbose, ambiguous, or imprecise — the TUI shows the topic, so a concise, well-scoped phrasing is more useful than a sprawling one. Preserve the meaning; do not change scope.

**Target length: 5–8 words.** Below 5, the topic is usually too vague to identify what you're working on; above 8, it crowds the TUI and signals the work isn't really one discrete thing. Use 9–12 words sparingly when a shorter phrasing genuinely loses scope.

If the original is already in that range, unambiguous, and action-oriented, pass it through verbatim. When you do re-phrase, briefly note what you changed in your reply so the user can correct you if you drifted.

Then update your own working model: the topic you just pushed is now the active focus. When you finish it, call `/pop`. If the user asks "what are you working on?", read the top of the stack rather than guessing.

If the command fails (no `stack` binary on PATH, etc.), tell the user what went wrong and stop.

## When the agent should self-invoke

Don't wait for the user to type `/push`. When you start a discrete sub-task whose state would be useful to track — kicking off a multi-step refactor, beginning a debugging investigation, taking on a side task — push it yourself via the Skill tool. The TUI shows the user what you're working on; an empty stack while you're deep in a sub-task is a missed signal.

Constraints:

- **Only push what justifies a line in the TUI.** Trivial tool calls (a single grep, one quick read, a short clarifying question) don't merit a push. Multi-step or multi-turn work does.
- **Don't auto-push on every user message.** Push when you're starting work, not when the user is just talking.

## Attaching context to a pushed item

A pushed topic is just a short label. When the *why* — a blocker, a link, a resume condition, the active mental model — would help on the next encounter, attach it as context after the push:

```
stack context top "waiting on review of PR #123"
```

Pipe in longer notes (multi-line OK) with the `-` form:

```
echo "...notes..." | stack context top -
```

When to attach context (don't reflexively; the topic alone is usually enough):

- **Tabling for later.** If you're popping or moving on without finishing, capture the resume condition so the next visit doesn't start from a blank slate.
- **The reason isn't obvious from the topic.** If the topic line reads fine on its own ("Fix login bug"), no context needed. If it points at a non-obvious blocker, link, or rationale, attach it.
- **Handoff state.** When you've reached a sub-decision or were reading specific files, jot the file paths and the conclusion so a future turn can resume mid-thought.

Keep contexts tight — they exceed 1 KB only by accident; the panel shows them inline. Update or `--clear` them as the situation changes.
