---
name: stack
description: Open or close the live tmux TUI pane for the current Claude Code session's stack. `/stack open` spawns the pane (idempotent — does nothing if one already exists); `/stack close` dismisses it. Use when you want the stack visible without pushing anything, or when you want the pane out of the way.
argument-hint: open|close
---

Run the following command via the Bash tool, then relay its stdout verbatim to the user.

```
stack $ARGUMENTS
```

`$ARGUMENTS` will be `open` or `close`. The CLI handles both cases:

- `stack open` — if no TUI pane exists for this tmux session, spawn one (split direction is decided automatically from the window's aspect ratio). If one already exists, prints `TUI pane already open` and exits 0.
- `stack close` — kills every `@stack-tui` pane in the current tmux session.

Outside tmux, `open` prints a hint that you need a tmux session and exits 0. The stack itself is unaffected by either subcommand — they only manage the visual pane.

If the user typed `/stack` with no argument, ask them whether they meant `open` or `close` before running anything.
