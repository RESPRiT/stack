#!/usr/bin/env bash
# Claude Code SessionStart hook — fires at session launch and on
# resume/clear/compact, before the user submits any prompt.
#
# The Go binary inspects the JSON payload (`source` field) and:
#   - closes the @stack-tui pane on /clear
#   - retargets the @stack-tui pane to the new session on /resume
#   - no-ops on startup/compact
#
# Install: register in ~/.claude/settings.json under
#   "hooks": { "SessionStart": [ { "hooks": [ { "type": "command",
#                "command": "/path/to/stack/hooks/session-start.sh" }
#              ] } ] }

set -e
exec "${STACK_BIN:-stack}" hook session-start
