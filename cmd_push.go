package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"stack/internal/session"
	"stack/internal/store"
	"stack/internal/tmuxpane"
)

func cmdPush(args []string) error {
	fs := flag.NewFlagSet("push", flag.ContinueOnError)
	sess := fs.String("session", "", "session id (default: $CLAUDE_CODE_SESSION_ID or newest)")
	name := fs.String("stack", store.Main, "target stack (created on first push)")
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
	if *name == "" {
		return errors.New("stack name cannot be empty")
	}

	id, dir, err := session.ResolveOrCreate(*sess)
	if err != nil {
		return err
	}
	s, err := store.Push(dir, *name, topic)
	if err != nil {
		return err
	}
	fmt.Printf("pushed onto %s (depth %d): %s\n", targetLabel(id, *name), s.Depth(*name), topic)
	if err := tmuxpane.EnsureRunning(id); err != nil {
		fmt.Fprintf(os.Stderr, "stack: tui auto-spawn failed: %v\n", err)
	}
	return nil
}
