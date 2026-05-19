---
name: stack
description: Open or close the live tmux TUI pane for the current Claude Code session's stack. `/stack` (or `/stack open`) spawns the pane (idempotent — does nothing if one already exists); `/stack close` dismisses it. Use when you want the stack visible without pushing anything, or when you want the pane out of the way.
argument-hint: "[open|close]"
---

If `$ARGUMENTS` is empty, treat it as `open`. Otherwise pass it through verbatim. Then run the resolved command via the Bash tool and relay its stdout verbatim to the user.

```
stack <resolved-argument>
```

CLI behavior:

- `stack open` — if no TUI pane exists for this tmux session, spawn one (split direction is decided automatically from the window's aspect ratio). If one already exists, prints `TUI pane already open` and exits 0.
- `stack close` — kills every `@stack-tui` pane in the current tmux session.

Outside tmux, `open` prints a hint that you need a tmux session and exits 0. The stack itself is unaffected by either subcommand — they only manage the visual pane.
