# stack — agent instructions

## Versioning

Pre-1.0 the version format is `0.MINOR.PATCH` (currently `0.2.x`), held in
the `Version` constant in `main.go` and surfaced in both the TUI header and
`stack version`.

**Bump the patch generously.** Any commit that ships user-visible behavior
— a new subcommand, a default change, a UX tweak, a bugfix that changes CLI
output, a new TUI element, a different error message — should bump `Version`
by one patch in the same commit. Mention the bump in the commit body.

Don't bump for:

- Pure refactors that preserve behavior (variable renames, code moves,
  package reshuffles).
- Documentation, wiki, or comment-only changes.
- Test-only additions that don't change shipped behavior.

If you forget a bump on a behavior-changing commit, the next such commit can
include two bumps in its message; record both `Version` deltas in the body
so the changelog stays honest.

Minor bumps (`0.X` → `0.X+1`) are user-driven — typically a release boundary
or a coherent surface change. Don't auto-bump minor.

## Dev loop

Default to HMR. At the start of any session that involves TUI iteration,
turn the watcher daemon on:

```
stack hmr on
```

Each `.go` save triggers a debounced `go install .` and respawns the
`@stack-tui` pane within ~600 ms — no need for manual `go install` +
`stack close && stack open` cycles. Build errors stream to
`~/.stack/hmr.log`; if the TUI stops updating after a save, tail the log.

Only fall back to manual rebuild when:

- The change is non-TUI (CLI subcommand, internal package called by
  another CLI surface) — HMR respawns the TUI but doesn't affect CLI
  invocations that aren't in a watched pane.
- You need the `go install` error directly in your tool output (e.g.,
  to paste into a reply or reason about the failure inline).
- You're intentionally testing the pane lifecycle itself (close/open,
  reflow, auto-spawn) and need a known-good binary to start from.

Toggle from inside the TUI via the `h` keybind; toggle from a shell via
`stack hmr` (no arg = toggle, `on`/`off`/`status` are explicit).

## Verifying TUI changes

After any code change that affects what the Bubble Tea TUI renders, capture
the pane and read the output back:

```
tmux capture-pane -t <pane-id> -p | cat -n
```

`cat -n` numbers the lines so layout shifts are obvious — off-by-one gap
rows, missing blank separators between sections, wrong scroll offsets, the
empty-state vs non-empty-state divergence all show up immediately. Counting
the rows in the diff between what `View()` emits and what tmux actually shows
has caught several layout bugs in this codebase. The cost is one bash call;
the alternative is shipping a layout bug and finding out from a user.

Capture both states when the change touches a conditional path (e.g., the
empty-stack branch differs from the non-empty one) — verifying only the
"easy" state lets the broken one ship.
