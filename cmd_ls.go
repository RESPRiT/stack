package main

import (
	"errors"
	"flag"
	"fmt"

	"stack/internal/session"
	"stack/internal/store"
)

func cmdLs(args []string) error {
	fs := flag.NewFlagSet("ls", flag.ContinueOnError)
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
	if len(s.Topics) == 0 {
		fmt.Printf("%s: (empty)\n", id)
		return nil
	}
	fmt.Printf("%s (depth %d, top first):\n", id, len(s.Topics))
	for i := len(s.Topics) - 1; i >= 0; i-- {
		marker := "  "
		if i == len(s.Topics)-1 {
			marker = "▶ "
		}
		fmt.Printf("%s%s\n", marker, s.Topics[i])
	}
	return nil
}
