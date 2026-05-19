package main

import (
	"fmt"
	"os"
)

// Version is the user-visible release marker. Shown in the TUI header
// and by `stack --version`. Bumped manually per release; pre-1.0 while
// the surface is still iterating.
const Version = "0.1.24"

const usage = `stack — a topic stack for coding-agent sessions.

Usage:
  stack                    alias for stack ls (default action)
  stack push <topic>...    push a topic onto the current session's stack
  stack pop                pop the top topic
  stack ls [-v]            list the stack, top first; -v also prints each topic's context
  stack edit rename <pos> <text>   rename the topic at position (1-based top-first, or top|bottom)
  stack edit move <from> <to>      reorder a topic; positions as above
  stack context <pos>              print the context attached to the topic at <pos>
  stack context <pos> <text>...    set the context inline (positional args joined with spaces)
  stack context <pos> -            read the context from stdin
  stack context <pos> --clear      remove the context (item reverts to a bare string on disk)
  stack tui                live TUI view of the stack
  stack open               spawn the TUI pane (inside tmux), idempotent
  stack close              dismiss any open TUI pane(s) for this session
  stack reflow             reposition the TUI pane for the current terminal size
  stack hmr [on|off|status]   toggle hot-reload of the TUI on .go changes (no arg = toggle)
  stack version            print the version
  stack help               show this help

Session resolution: --session flag, then $CLAUDE_CODE_SESSION_ID, then the
most-recently-modified session under ~/.stack/sessions/.
`

func main() {
	// Bare `stack` aliases to `stack ls` — the most common interactive
	// thing to do is glance at the current stack; making that the
	// default beats printing usage.
	if len(os.Args) < 2 {
		if err := cmdLs(nil); err != nil {
			fmt.Fprintf(os.Stderr, "stack: %v\n", err)
			os.Exit(1)
		}
		return
	}

	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "push":
		err = cmdPush(args)
	case "pop":
		err = cmdPop(args)
	case "ls", "list":
		err = cmdLs(args)
	case "edit":
		err = cmdEdit(args)
	case "tui":
		err = cmdTUI(args)
	case "open":
		err = cmdOpen(args)
	case "close":
		err = cmdClose(args)
	case "reflow":
		err = cmdReflow(args)
	case "hmr":
		err = cmdHMR(args)
	case "hook":
		err = cmdHook(args)
	case "context":
		err = cmdContext(args)
	case "help", "-h", "--help":
		fmt.Print(usage)
	case "version", "--version", "-v":
		fmt.Println(Version)
	default:
		fmt.Fprintf(os.Stderr, "stack: unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "stack: %v\n", err)
		os.Exit(1)
	}
}
