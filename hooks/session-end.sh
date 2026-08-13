#!/usr/bin/env bash
# Claude Code SessionEnd hook — fires when a session ends (exit, /clear,
# logout, resume-handoff, …), with the trigger in the JSON payload's
# `reason` field.
#
# The Go binary inspects the payload and:
#   - no-ops on reason=resume (SessionStart retargets the pane instead)
#   - closes the @stack-tui pane in the firing window on every other
#     reason, so the pane never outlives its parent session
#
# Install: register in ~/.claude/settings.json under
#   "hooks": { "SessionEnd": [ { "hooks": [ { "type": "command",
#                "command": "/path/to/stack/hooks/session-end.sh" }
#              ] } ] }

set -e
exec "${STACK_BIN:-stack}" hook session-end
