---
description: Push a topic onto the current session's stack
argument-hint: <topic>
allowed-tools: Bash(stack push:*)
---

Run `stack push "$ARGUMENTS"` to record what you're focused on next. The
TUI (if running) will pick this up via its fsnotify watcher.
