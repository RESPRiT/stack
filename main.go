package main

import (
	"fmt"
	"os"
)

// Version is the user-visible release marker. Shown in the TUI header
// and by `stack --version`. Bumped manually per release; pre-1.0 while
// the surface is still iterating.
const Version = "0.2.4"

const usage = `stack — topic stacks for coding-agent sessions.

A session holds named stacks. "main" always exists and is the session's
current focus; commands target it unless --stack says otherwise. Named
stacks are created on first push and persist when emptied (rm deletes).

Usage:
  stack                    alias for stack ls (default action)
  stack push [--stack <name>] <topic>...   push a topic (named stacks created on first push)
  stack pop [--stack <name>]               pop the top topic
  stack ls [-v] [--stack <name> | --all]   list one stack (default main) or every stack;
                                           -v also prints each topic's ref
  stack focus [--stack <name>]             print the markdown context-switch header
                                           naming the stack's current top; read-only,
                                           meant to be chained after push/pop/mv
  stack stacks             list the session's stack names and depths
  stack rm <name>          delete a stack ("main" is undeletable)
  stack mv [<pos>] [--from <name>] [--to <name>]   move the item at <pos> (default top)
                                           from one stack onto another's top, ref included;
                                           both flags default to main
  stack edit rename [--stack <name>] <pos> <text>   rename the topic at position
                                           (1-based top-first, or top|bottom)
  stack edit move [--stack <name>] <from> <to>      reorder a topic; positions as above
  stack ref [--stack <name>] <pos>         print the ref attached to the topic at <pos>
  stack ref [--stack <name>] <pos> <text>...  set the ref inline (args joined with spaces)
  stack ref [--stack <name>] <pos> -          read the ref from stdin
  stack ref [--stack <name>] <pos> --clear    remove the ref
  stack tui                live TUI view; inside an @stack-tui pane runs the foreground
                           process, outside it auto-spawns one (= stack tui open)
  stack tui open           spawn the @stack-tui pane (idempotent — no-op if one exists)
  stack tui close          dismiss any open TUI pane(s) for this session
  stack tui reflow [<window>] [--all]   reflow the TUI pane for its window's dims
                           (default: this window; --all: every window with a pane)
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
	case "focus":
		err = cmdFocus(args)
	case "ls", "list":
		err = cmdLs(args)
	case "stacks":
		err = cmdStacks(args)
	case "rm":
		err = cmdRm(args)
	case "mv":
		err = cmdMv(args)
	case "edit":
		err = cmdEdit(args)
	case "tui":
		err = cmdTUI(args)
	case "hmr":
		err = cmdHMR(args)
	case "hook":
		err = cmdHook(args)
	case "ref":
		err = cmdRef(args)
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
