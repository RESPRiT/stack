package main

import (
	"errors"
	"flag"
	"fmt"
	"strings"

	"stack/internal/session"
	"stack/internal/store"
)

func cmdPush(args []string) error {
	fs := flag.NewFlagSet("push", flag.ContinueOnError)
	sess := fs.String("session", "", "session id (default: $CLAUDE_CODE_SESSION_ID or newest)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return errors.New("push requires a topic argument")
	}
	topic := strings.TrimSpace(strings.Join(rest, " "))
	if topic == "" {
		return errors.New("topic cannot be empty")
	}

	id, dir, err := session.ResolveOrCreate(*sess)
	if err != nil {
		return err
	}
	s, err := store.Push(dir, topic)
	if err != nil {
		return err
	}
	fmt.Printf("pushed onto %s (depth %d): %s\n", id, len(s.Topics), topic)
	return nil
}
