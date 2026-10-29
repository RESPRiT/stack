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

`stack` maintains per-session **named LIFO topic stacks** — a visible
record of what the agent is working on now and what's parked beside it.

- **`main` is the focus.** It always exists; commands target it unless
  `--stack <name>` says otherwise, and its top is the session's current
  work — the only item that earns the `▶` marker.
- **Push = new focus.** `stack push <topic>` puts a topic on top of
  `main`; the old top stays underneath as suspended work to return to.
  Starting a nested sub-task means pushing, not replacing.
- **Pop = closing the loop.** `stack pop` removes the top topic and
  prints a `next:` line naming what you fall back to; the next-down item
  (or empty) becomes the active focus again.
- **Named stacks hold parallel threads** — parked ideas, a deferred
  workstream (`stack push --stack backlog "<topic>"`). They're created
  on first push, persist when emptied, and are removed only by
  `stack rm <name>`; `stack stacks` lists names and depths. Their tops
  are just the most recent item, not the session's focus.
- **Items may carry a ref** — a short note (blocker, link, resume state)
  attached via `stack ref`, shown inline in the TUI.
- **Mistakes are undoable.** Every mutation (push, pop, mv, rm, edit,
  ref) is logged; `stack undo` reverts the most recent one, and repeated
  undos step further back (up to the last 100 logged ops). `stack
  history` lists popped topics newest first (`--all` for every op) — the
  session's record of finished work, kept for its last 500 pops.
- **Every focus change is announced.** `push`, `pop`, `mv`, and `undo` print a
  markdown header naming `main`'s new top whenever they move it, and the
  agent echoes it into its reply so the switch is visible in the
  transcript, not just in the pane.

## Announcing a context switch

A stack only helps if the reader can tell when attention moved. Every
command that moves `main`'s top prints that marker — a rule, a `[main · depth N]` counter, the top
topic in bold, and its ref when it has one:

```
`━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━`
`                                            [main · depth 2]`
`▶` **Refactor the session resolver**

> **Ref:** blocked on PR #123 review; see internal/webhook/retry.go
```

The user does not see Bash output, so echo it verbatim in the turn's
final reply, at the point where the new topic begins: wrap up the previous
topic above it, and put only the new focus beneath it. **Never draw the rule yourself** — its width is measured
from the live tmux pane, and a hand-built one comes out the wrong length
often enough that the header stops reading as a landmark, which is the
only thing it is for. Write the orientation prose underneath it; the
binary owns the scaffold, you own the words.

`stack focus` prints the same header on demand. It reads state and never
mutates it, so it is the way to re-orient outside a push or pop: after a `/clear`, at the start of a
resumed session, or when the user asks what you're working on. Reach for
it instead of guessing from conversation history.

Each Claude Code session gets its own stacks, resolved from
`$CLAUDE_CODE_SESSION_ID` and stored at
`~/.stack/sessions/<id>/stack.json`. The TUI pane this skill manages is a
live tmux view: the `main` section first (top-first, `▶` marks the top),
then one titled section per named stack; it renders state but never
mutates it. The first `push` of a session auto-spawns the pane, and the
`/push` and `/pop` skills wrap the corresponding commands.

## CLI behavior

- `stack tui open` — if no TUI pane exists for this tmux session, spawn one (split direction is decided automatically from the window's aspect ratio). If one already exists, prints `TUI pane already open` and exits 0.
- `stack tui close` — kills every `@stack-tui` pane in the current tmux session.
- `stack undo [--force]` — restore the state before the last logged op. If `stack.json` isn't what that op left (a hand edit, or a write that failed to log), it refuses and exits 1; `--force` restores anyway, discarding everything since that op. When the undo changes `main`'s top it prints the context-switch header like `pop` does. Undo is for correcting a wrong push/pop/mv the user points out, not for routine navigation — finishing a task is still `pop`.
- `stack history [-v] [--all] [-n N]` — popped topics newest first (default 20); `--all` adds every op, `-v` prints refs. Read-only; the log is `~/.stack/sessions/<id>/history.jsonl` — the last 100 ops stay undoable, and older pops are kept (up to 500) for this listing only. Undoing a pop removes it from the list.
- `stack focus [--stack <name>]` — print the context-switch header for that stack's top (default `main`); read-only. `--width <cols>` overrides the pane measurement.

Outside tmux, `stack tui open` prints a hint that you need a tmux session and exits 0. The stack itself is unaffected by either subcommand — they only manage the visual pane.
