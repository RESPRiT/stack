---
name: pop
description: Pop the top topic from the current Claude Code session's stack. Invoke when finishing the sub-task currently on top — the next-down topic (or empty) becomes the new focus.
---

Run the following command via the Bash tool.

```
stack pop
```

The topic you just popped is no longer the active focus. A pop from `main` prints the context-switch header for whatever it uncovered — see **The context-switch header** below. A pop from a named stack (`--stack backlog`) doesn't move the focus and prints no header.

A pop that fails because the stack was already empty exits 1 and prints no header, since no switch happened. A pop that succeeds and empties the stack does print one, reading `[main · empty]` — that's the signal to ask the user what's next.

The stack is LIFO: pop removes the most recent push, and the item beneath resumes as the focus. See the `/stack` skill for the full model.

Plain `stack pop` always targets `main`, the session focus. To clear an item from a named stack (e.g. a done `backlog` entry), pass `--stack <name>` — that never touches the focus.

If the command fails because the stack is empty, tell the user "(stack is empty)" and stop — don't infer that something must be popped.

## The context-switch header

When `main`'s top changes, `stack pop` prints a markdown header after its own
output — a rule, a `[main · depth N]` counter, the topic in bold, and its ref if
it carries one — followed by a one-line reminder addressed to you:

```
`━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━`
`                                            [main · depth 2]`
`▶` **Refactor the session resolver**

> **Ref:** blocked on PR #123 review; see internal/webhook/retry.go

↑ The user does not see tool output. Echo the header above verbatim …
```

**The user does not see Bash output.** The header reaches them only if you put
it in your reply, so a header left in tool output is a missed switch. It goes
at the top of this turn's **final** reply — the message that ends the turn, not
an interim note before more tool calls. If you keep working after the pop,
carry it to the end. If the focus moves more than once in a turn (two promotes,
a pop then a push), echo only the last header. Leave the `↑` reminder line out;
it is for you.

Echo it verbatim. **Never draw the rule yourself.** Its width is measured
from the live tmux pane, and a hand-built one comes out the wrong length often
enough that the header stops reading as a landmark — which is the only thing it
is for.

Underneath it, write two or three sentences re-orienting onto the resumed
topic: where that work stood when it was suspended, and what the next concrete
step is. **The ref is the handoff note** — the item beneath may have been
parked for a long stretch, and the ref is what the parking turn thought its
future self would need. Read it and say what it means for the next step rather
than restating it back.

When the header reads `[main · empty]`, there is nothing to re-orient onto.
Say the stack is clear and ask what's next; don't invent a topic to fill it.

Print the header once, in the turn the pop happens. Later turns on the resumed
topic get no header; a rule that appears every turn marks nothing.

## When the agent should self-invoke

When you finish a sub-task that sits on top of the stack, pop it without waiting for the user to type `/pop`. Pair the pop with the moment you deliver the result — "task done, here's the outcome, popping" — rather than deferring to end-of-turn or until the user prompts. A stale top in the TUI is a worse signal than a momentary empty stack.

Pop when:

- The sub-task that defines the current top is *complete* — committed, validated, or explicitly handed off.
- The item is being explicitly tabled for later.

Don't pop just because:

- You finished one tool call within a larger task. The task isn't done.
- The conversation drifted briefly. Drifts come back.
- You're about to push something new. Push goes on top; the old top stays underneath.

**It is worth emphasizing, again: the temptation to `/pop` is high since it clears the stack, but it is OK for items on the stack to accumulate! Reserve `/pop` for truly closed loops.**