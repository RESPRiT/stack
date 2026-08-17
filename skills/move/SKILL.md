---
name: move
description: Move an item between the current Claude Code session's named stacks — promote a parked item to the main focus, or park the current focus onto a named stack — atomically, ref included. Invoke when an item should change stacks rather than being finished (/pop) or duplicated (/push).
argument-hint: "[<pos>] --from <stack> | --to <stack>"
---

Run the resolved command via the Bash tool and relay its stdout verbatim to the user.

```
stack mv [<pos>] [--from <name>] [--to <name>] && stack focus
```

Chain `stack focus` whenever `main` is one of the two endpoints — both a promote and a park change the focus, so both are context switches worth announcing. Drop it for a move between two named stacks, which leaves the focus alone.

- `<pos>` addresses the item in the **source** stack (`top`, `bottom`, or 1-based top-first); omitted means `top`.
- `--from` and `--to` both default to `main`, so the two common motions are terse:
  - **Promote** a parked item to the focus: `stack mv --from backlog`
  - **Park** the current focus for later: `stack mv --to backlog`
- The item lands on top of the destination, and its ref travels with it — this is what makes `/move` better than a manual pop + re-push, which drops the ref. The destination stack is created if it doesn't exist yet.

Then update your working model, mirroring `/push` and `/pop`:

- After a **promote**, the moved item is the new focus.
- After a **park**, the next item down on `main` is the focus again. The header names it, so you don't need a separate `stack ls`. If `main` is now empty, ask the user what's next.

## The context-switch header

`stack focus` prints a markdown header naming whatever now sits on top of `main` — a rule, a `[main · depth N]` counter, the topic in bold, and its ref if it carries one:

```
`━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━`
`                                            [main · depth 2]`
`▶` **Refactor the session resolver**

> **Ref:** blocked on PR #123 review; see internal/webhook/retry.go
```

Echo it verbatim, directly after the `mv` output. **Never draw the rule yourself.** Its width is measured from the live tmux pane, and a hand-built one comes out the wrong length often enough that the header stops reading as a landmark — which is the only thing it is for.

Underneath it, write two or three sentences of orientation. A promote and a park want different prose:

- **Promoted** — the item has been parked, possibly for a while, and its ref travelled with it. Read the ref, say where the work stood, and name the next concrete step.
- **Parked** — you're falling back to suspended work on `main`. Orient onto *that* item, not the one you just parked; the parked topic is done being discussed.

Print the header once, in the turn the move happens. Later turns on the same topic get no header; a rule that appears every turn marks nothing.

Don't use `/move` to reorder within one stack — that's `stack edit move`. A same-stack `mv` errors by design.
