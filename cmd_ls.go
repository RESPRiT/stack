package main

import (
	"errors"
	"flag"
	"fmt"
	"strings"

	"stack/internal/session"
	"stack/internal/store"
)

func cmdLs(args []string) error {
	fs := flag.NewFlagSet("ls", flag.ContinueOnError)
	sess := fs.String("session", "", "session id (default: $CLAUDE_CODE_SESSION_ID or newest)")
	name := fs.String("stack", store.Main, "stack to list")
	all := fs.Bool("all", false, "list every stack, main first")
	verbose := fs.Bool("v", false, "also print each topic's ref on the following line")
	if err := fs.Parse(args); err != nil {
		return err
	}

	id, dir, err := session.Resolve(*sess)
	if err != nil {
		if errors.Is(err, session.ErrNoSession) {
			fmt.Println("(no stacks yet)")
			return nil
		}
		return err
	}
	s, err := store.Load(dir)
	if err != nil {
		return err
	}

	if *all {
		for i, n := range s.Stacks {
			if i > 0 {
				fmt.Println()
			}
			printStack(id, n, *verbose)
		}
		return nil
	}

	topics, ok := s.Get(*name)
	if !ok {
		return fmt.Errorf("no stack named %q (have: %s)", *name, strings.Join(s.Names(), ", "))
	}
	printStack(id, store.Named{Name: *name, Topics: topics}, *verbose)
	return nil
}

// printStack renders one named stack, top first. Main keeps the classic
// header (session id only) and the ▶ current-focus marker on its top;
// other stacks are labeled "<id>/<name>" and carry no ▶ — their top is
// just the most recent item, not the session's current work.
func printStack(id string, n store.Named, verbose bool) {
	label := targetLabel(id, n.Name)
	if len(n.Topics) == 0 {
		fmt.Printf("%s: (empty)\n", label)
		return
	}
	fmt.Printf("%s (depth %d, top first):\n", label, len(n.Topics))
	for i := len(n.Topics) - 1; i >= 0; i-- {
		marker := "  "
		if n.Name == store.Main && i == len(n.Topics)-1 {
			marker = "▶ "
		}
		// Ref-bearing items get a trailing · so the user can tell at a
		// glance which entries carry an attached ref.
		refMark := ""
		if n.Topics[i].Ref != "" {
			refMark = " ·"
		}
		fmt.Printf("%s%s%s\n", marker, n.Topics[i].Topic, refMark)
		if verbose && n.Topics[i].Ref != "" {
			for _, line := range strings.Split(n.Topics[i].Ref, "\n") {
				fmt.Printf("    %s\n", line)
			}
		}
	}
}
