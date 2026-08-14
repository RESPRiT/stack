package main

import (
	"errors"
	"flag"
	"fmt"

	"stack/internal/session"
	"stack/internal/store"
)

// cmdStacks implements `stack stacks`: the session's stack names and
// depths, main first, then creation order.
func cmdStacks(args []string) error {
	fs := flag.NewFlagSet("stacks", flag.ContinueOnError)
	sess := fs.String("session", "", "session id (default: $CLAUDE_CODE_SESSION_ID or newest)")
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
	fmt.Printf("%s:\n", id)
	for _, n := range s.Stacks {
		fmt.Printf("  %s (depth %d)\n", n.Name, len(n.Topics))
	}
	return nil
}

// cmdRm implements `stack rm <name>`: delete a named stack outright.
// Main is undeletable; emptied stacks otherwise persist until removed
// here.
func cmdRm(args []string) error {
	fs := flag.NewFlagSet("rm", flag.ContinueOnError)
	sess := fs.String("session", "", "session id (default: $CLAUDE_CODE_SESSION_ID or newest)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("rm requires exactly one stack name")
	}
	name := fs.Arg(0)
	id, dir, err := session.Resolve(*sess)
	if err != nil {
		if errors.Is(err, session.ErrNoSession) {
			return errors.New("no session to operate on")
		}
		return err
	}
	depth, err := store.Delete(dir, name)
	if err != nil {
		return err
	}
	fmt.Printf("deleted stack %q (%d item(s)) in %s\n", name, depth, id)
	return nil
}
