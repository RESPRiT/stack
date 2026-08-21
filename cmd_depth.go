package main

import (
	"errors"
	"flag"
	"fmt"

	"stack/internal/session"
	"stack/internal/store"
)

// cmdDepth implements `stack depth [--stack <name>] [--total]`: print
// the topic count of one stack (default main) — or, with --total, the
// sum over every stack in the session — as a bare integer. The
// scripting counterpart to `stack stacks`: stable single-number output
// for callers (hooks, statuslines, prompts) that branch on stack state
// without parsing human-readable listings.
func cmdDepth(args []string) error {
	fs := flag.NewFlagSet("depth", flag.ContinueOnError)
	sess := fs.String("session", "", "session id (default: $CLAUDE_CODE_SESSION_ID or newest)")
	name := fs.String("stack", store.Main, "stack to measure")
	total := fs.Bool("total", false, "sum across every stack in the session")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("depth takes no positional args (got %q)", fs.Arg(0))
	}
	_, dir, err := session.Resolve(*sess)
	if err != nil {
		if errors.Is(err, session.ErrNoSession) {
			fmt.Println(0) // no sessions at all — nothing anywhere
			return nil
		}
		return err
	}
	s, err := store.Load(dir)
	if err != nil {
		return err
	}
	if *total {
		fmt.Println(s.TotalDepth())
		return nil
	}
	topics, ok := s.Get(*name)
	if !ok {
		return fmt.Errorf("no stack named %q (have: %v)", *name, s.Names())
	}
	fmt.Println(len(topics))
	return nil
}
