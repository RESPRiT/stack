# stack — agent instructions

## Versioning

Pre-1.0 the version format is `0.MINOR.PATCH` (currently `0.1.x`), held in
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
