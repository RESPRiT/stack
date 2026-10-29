---
name: move
description: Move an item between the current Claude Code session's named stacks — promote a parked item to the main focus, or park the current focus onto a named stack — atomically, ref included. Invoke when an item should change stacks rather than being finished (/pop) or duplicated (/push).
argument-hint: "--from <stack> | --to <stack> [<pos>]"
---

Run the resolved command via the Bash tool.

```
stack mv [--from <name>] [--to <name>] [<pos>]
```

When `main` is one of the two endpoints, `mv` prints the context-switch header — both a promote and a park change the focus. A move between two named stacks leaves the focus alone and prints no header.

- `<pos>` addresses the item in the **source** stack (`top`, `bottom`, or 1-based top-first); omitted means `top`. Flags may go before or after it.
- `--from` and `--to` both default to `main`, so the two common motions are terse:
  - **Promote** a parked item to the focus: `stack mv --from backlog`
  - **Park** the current focus for later: `stack mv --to backlog`
- The item lands on top of the destination, and its ref travels with it — this is what makes `/move` better than a manual pop + re-push, which drops the ref. The destination stack is created if it doesn't exist yet.

Then update your working model, mirroring `/push` and `/pop`:

- After a **promote**, the moved item is the new focus.
- To promote several items, run one `mv` per item. Each prints a header; only the last one matters.
- After a **park**, the next item down on `main` is the focus again. The header names it, so you don't need a separate `stack ls`. If `main` is now empty, ask the user what's next.

## The context-switch header

When `main`'s top changes, `stack mv` prints a markdown header after its own output — a rule, a `[main · depth N]` counter, the topic in bold, and its ref if it carries one — followed by a one-line reminder addressed to you:

```
`━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━`
`                                            [main · depth 2]`
`▶` **Refactor the session resolver**

> **Ref:** blocked on PR #123 review; see internal/webhook/retry.go

↑ The user does not see tool output. Echo the header above verbatim …
```

**The user does not see Bash output.** The header reaches them only if you put it in your reply, so a header left in tool output is a missed switch. It goes in this turn's **final** reply — the message that ends the turn, not an interim note before more tool calls.

**The header is a delimiter, not a masthead.** Everything above it belongs to the previous topic; everything below it belongs to the new focus. Finish the old topic first — the result you just delivered, answers to the user's last message — then the header, then orientation on the new focus. Nothing about the old topic goes beneath the header. When there is nothing left to say about the old topic, the header opens the reply.

If you keep working after the move, carry the header to the end. If the focus moves more than once in a turn (two promotes, a pop then a push), echo only the last header. Leave the `↑` reminder line out; it is for you.

Echo it verbatim. **Never draw the rule yourself.** Its width is measured from the live tmux pane, and a hand-built one comes out the wrong length often enough that the header stops reading as a landmark — which is the only thing it is for.

Underneath it, write two or three sentences of orientation. A promote and a park want different prose:

- **Promoted** — the item has been parked, possibly for a while, and its ref travelled with it. Read the ref, say where the work stood, and name the next concrete step.
- **Parked** — you're falling back to suspended work on `main`. Orient onto *that* item, not the one you just parked; the parked topic is done being discussed.

Print the header once, in the turn the move happens. Later turns on the same topic get no header; a rule that appears every turn marks nothing.

Don't use `/move` to reorder within one stack — that's `stack edit move`. A same-stack `mv` errors by design.
