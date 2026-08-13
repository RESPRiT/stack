---
name: stack
description: Open or close the live tmux TUI pane for the current Claude Code session's stack. `/stack` (or `/stack open`) spawns the pane (idempotent — does nothing if one already exists); `/stack close` dismisses it. Use when you want the stack visible without pushing anything, or when you want the pane out of the way.
argument-hint: "[open|close]"
---

If `$ARGUMENTS` is empty, treat it as `open`. Otherwise pass it through verbatim. Then run the resolved command via the Bash tool and relay its stdout verbatim to the user.

```
stack tui $ARGUMENTS
```

## How the stack works

`stack` maintains a per-session **LIFO topic stack** — a visible record of
what the agent is working on now and what's suspended beneath it.

- **Push = new focus.** `stack push <topic>` puts a topic on top; the old
  top stays underneath as suspended work to return to. Starting a nested
  sub-task means pushing, not replacing.
- **Pop = closing the loop.** `stack pop` removes the top topic; the
  next-down item (or empty) becomes the active focus again.
- **Items may carry a ref** — a short note (blocker, link, resume state)
  attached via `stack ref`, shown inline in the TUI.

Each Claude Code session gets its own stack, resolved from
`$CLAUDE_CODE_SESSION_ID` and stored at
`~/.stack/sessions/<id>/stack.json`. The TUI pane this skill manages is a
live tmux view of that stack (top-first, `▶` marks the top); it renders
state but never mutates it. The first `push` of a session auto-spawns the
pane, and the `/push` and `/pop` skills wrap the corresponding commands.

## CLI behavior

- `stack tui open` — if no TUI pane exists for this tmux session, spawn one (split direction is decided automatically from the window's aspect ratio). If one already exists, prints `TUI pane already open` and exits 0.
- `stack tui close` — kills every `@stack-tui` pane in the current tmux session.

Outside tmux, `stack tui open` prints a hint that you need a tmux session and exits 0. The stack itself is unaffected by either subcommand — they only manage the visual pane.
