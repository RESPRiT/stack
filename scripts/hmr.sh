#!/usr/bin/env bash
# scripts/hmr.sh — rebuild the `stack` binary on .go changes and respawn
# the TUI in a target tmux pane. State lives on disk, so the new process
# picks up exactly where the old one left off.
#
# Usage:
#   STACK_HMR_PANE=%103 [CLAUDE_SESSION_ID=demo] ./scripts/hmr.sh
set -euo pipefail

: "${STACK_HMR_PANE:?set STACK_HMR_PANE to a tmux pane id (e.g. %103)}"
: "${CLAUDE_SESSION_ID:=demo}"

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN="$REPO_ROOT/bin/stack"
cd "$REPO_ROOT"

if ! command -v fswatch >/dev/null; then
  echo "hmr: fswatch not found (brew install fswatch)" >&2
  exit 1
fi
if ! tmux display -p -t "$STACK_HMR_PANE" '#{pane_id}' >/dev/null 2>&1; then
  echo "hmr: tmux pane $STACK_HMR_PANE not found" >&2
  exit 1
fi

rebuild_and_respawn() {
  if ! go build -o "$BIN" . 2>&1; then
    echo "hmr: build failed — leaving running TUI in place" >&2
    return
  fi
  # -k kills the existing pane process; the pane itself is reused.
  tmux respawn-pane -k -t "$STACK_HMR_PANE" \
    "CLAUDE_SESSION_ID=$CLAUDE_SESSION_ID exec '$BIN' tui"
  echo "hmr: rebuilt + respawned $STACK_HMR_PANE @ $(date +%H:%M:%S)"
}

echo "hmr: watching $REPO_ROOT (Ctrl-C to stop)"
rebuild_and_respawn

# fswatch -o prints a number for each batched event set; we don't care
# about which files changed, just that something did.
fswatch -o -E \
  --exclude '/\.git(/|$)' \
  --exclude '/bin(/|$)' \
  --exclude '\.swp$' \
  --include '\.go$' \
  --include 'go\.(mod|sum)$' \
  . | while read -r _; do
    rebuild_and_respawn
  done
