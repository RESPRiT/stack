---
name: push
description: Push a topic onto the current Claude Code session's stack so the agent (and any live `stack tui` panel) knows what's now the active focus. Invoke when starting a discrete sub-task whose progress should be visible in the stack.
argument-hint: <topic>
---

Run the following command via the Bash tool.

```
stack push "$ARGUMENTS"
```

A push onto `main` prints the context-switch header for the new top — see
**The context-switch header** below for what to do with it. A push onto a named
stack (`--stack backlog`) doesn't move the focus and prints no header.

You may re-phrase `$ARGUMENTS` before passing it to `stack push` when the original is verbose, ambiguous, or imprecise — the TUI shows the topic, so a concise, well-scoped phrasing is more useful than a sprawling one. Preserve the meaning; do not change scope.

**Target length: 5–8 words.** Below 5, the topic is usually too vague to identify what you're working on; above 8, it crowds the TUI and signals the work isn't really one discrete thing. Use 9–12 words sparingly when a shorter phrasing genuinely loses scope.

If the original is already in that range, unambiguous, and action-oriented, pass it through verbatim. When you do re-phrase, briefly note what you changed in your reply so the user can correct you if you drifted.

Then update your own working model: the topic you just pushed is now the active focus. When you finish it, call `/pop`. If the user asks "what are you working on?", run `stack focus` and answer from its header rather than guessing.

The stack is LIFO: pushing suspends the old top beneath the new topic; popping resumes it. See the `/stack` skill for the full model.

To record something that is *not* the current focus — a parked idea, a someday task — push it to a named stack instead of `main`: `stack push --stack backlog "<topic>"`. Named stacks are created on their first push and don't disturb the `▶` focus. Reserve plain `/push` for work you're actually starting.

If the command fails (no `stack` binary on PATH, etc.), tell the user what went wrong and stop.

## The context-switch header

When `main`'s top changes, `stack push` prints a markdown header after its own
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
an interim note before more tool calls. If you keep working after the push,
carry it to the end. If the focus moves more than once in a turn (two promotes,
a pop then a push), echo only the last header. Leave the `↑` reminder line out;
it is for you.

Echo it verbatim. **Never draw the rule yourself.** Its width is measured
from the live tmux pane, and a hand-built one comes out the wrong length often
enough that the header stops reading as a landmark — which is the only thing it
is for.

Underneath it, write two or three sentences of orientation: what this topic
covers, why it's worth its own frame, and what you're doing first. A freshly
pushed topic has no ref, so this prose is the only context the reader gets —
say what "done" looks like.

Print the header once, in the turn the switch happens. Later turns on the same
topic get no header; a rule that appears every turn marks nothing.

## When the agent should self-invoke

Don't wait for the user to type `/push`. When you start a discrete sub-task whose state would be useful to track — kicking off a multi-step refactor, beginning a debugging investigation, taking on a side task — push it yourself via the Skill tool. The TUI shows the user what you're working on; an empty stack while you're deep in a sub-task is a missed signal.

Constraints:

- **Only push what justifies a line in the TUI.** Trivial tool calls (a single grep, one quick read, a short clarifying question) don't merit a push. Multi-step or multi-turn work does.
- **Don't auto-push on every user message.** Push when you're starting work, not when the user is just talking.

## Attaching a ref to a pushed item

A pushed topic is just a short label. When the *why* — a blocker, a link, a resume condition, the active mental model — would help on the next encounter, attach it as a ref. Pass it with the push so it lands in the same write and shows in the header:

```
stack push --ref "waiting on review of PR #123" "<topic>"
echo "...notes..." | stack push --ref - "<topic>"
```

To attach or update a ref on an item that's already on the stack, use `stack ref`:

```
stack ref top "waiting on review of PR #123"
echo "...notes..." | stack ref top -
```

When to attach a ref (don't reflexively; the topic alone is usually enough):

- **Tabling for later.** If you're popping or moving on without finishing, capture the resume condition so the next visit doesn't start from a blank slate.
- **The reason isn't obvious from the topic.** If the topic line reads fine on its own ("Fix login bug"), no ref needed. If it points at a non-obvious blocker, link, or rationale, attach it.
- **Handoff state.** When you've reached a sub-decision or were reading specific files, jot the file paths and the conclusion so a future turn can resume mid-thought.

Keep refs tight — they exceed 1 KB only by accident; the panel shows them inline. Update or `--clear` them as the situation changes.
