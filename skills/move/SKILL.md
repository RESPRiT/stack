---
name: move
description: Move an item between the current Claude Code session's named stacks — promote a parked item to the main focus, or park the current focus onto a named stack — atomically, ref included. Invoke when an item should change stacks rather than being finished (/pop) or duplicated (/push).
argument-hint: "[<pos>] --from <stack> | --to <stack>"
---

Run the resolved command via the Bash tool and relay its stdout verbatim to the user.

```
stack mv [<pos>] [--from <name>] [--to <name>]
```

- `<pos>` addresses the item in the **source** stack (`top`, `bottom`, or 1-based top-first); omitted means `top`.
- `--from` and `--to` both default to `main`, so the two common motions are terse:
  - **Promote** a parked item to the focus: `stack mv --from backlog`
  - **Park** the current focus for later: `stack mv --to backlog`
- The item lands on top of the destination, and its ref travels with it — this is what makes `/move` better than a manual pop + re-push, which drops the ref. The destination stack is created if it doesn't exist yet.

Then update your working model, mirroring `/push` and `/pop`:

- After a **promote**, the moved item is the new focus.
- After a **park**, the next item down on `main` is the focus again; run `stack ls` if you need to see what that is. If `main` is now empty, ask the user what's next.

Don't use `/move` to reorder within one stack — that's `stack edit move`. A same-stack `mv` errors by design.
