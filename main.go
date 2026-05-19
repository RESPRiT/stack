package main

import (
	"fmt"
	"os"
)

const usage = `stack — a topic stack for coding-agent sessions.

Usage:
  stack push <topic>...    push a topic onto the current session's stack
  stack pop                pop the top topic
  stack ls                 list the stack, top first
  stack tui                live TUI view of the stack
  stack reflow             reposition the TUI pane for the current terminal size
  stack help               show this help

Session resolution: --session flag, then $CLAUDE_CODE_SESSION_ID, then the
most-recently-modified session under ~/.stack/sessions/.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
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
	case "tui":
		err = cmdTUI(args)
	case "reflow":
		err = cmdReflow(args)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "stack: unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "stack: %v\n", err)
		os.Exit(1)
	}
}
