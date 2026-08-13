package main

import (
	"errors"
	"flag"
	"fmt"

	"stack/internal/session"
	"stack/internal/store"
)

func cmdPop(args []string) error {
	fs := flag.NewFlagSet("pop", flag.ContinueOnError)
	sess := fs.String("session", "", "session id (default: $CLAUDE_CODE_SESSION_ID or newest)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("pop takes no positional args (got %q)", fs.Arg(0))
	}

	id, dir, err := session.Resolve(*sess)
	if err != nil {
		if errors.Is(err, session.ErrNoSession) {
			return errors.New("no session to pop from")
		}
		return err
	}
	popped, s, err := store.Pop(dir)
	if err != nil {
		if errors.Is(err, store.ErrEmpty) {
			return fmt.Errorf("stack for %s is empty", id)
		}
		return err
	}
	fmt.Printf("popped from %s (depth %d): %s\n", id, len(s.Topics), popped.Topic)
	if popped.Ref != "" {
		fmt.Printf("  ref: %s\n", popped.Ref)
	}
	if n := len(s.Topics); n > 0 {
		fmt.Printf("  next: %s\n", s.Topics[n-1].Topic)
	}
	return nil
}
