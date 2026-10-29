package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
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
	ref := fs.String("ref", "", "attach a ref to the pushed item (\"-\" reads stdin)")
	quiet := fs.Bool("quiet", false, "skip the context-switch header")
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
	item := store.Item{Topic: topic, Ref: strings.TrimSpace(*ref)}
	if *ref == "-" {
		raw, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
		item.Ref = strings.TrimRight(string(raw), "\n")
	}

	id, dir, err := session.ResolveOrCreate(*sess)
	if err != nil {
		return err
	}
	s, err := store.PushItem(dir, *name, item)
	if err != nil {
		return err
	}
	fmt.Printf("pushed onto %s (depth %d): %s\n", targetLabel(id, *name), s.Depth(*name), topic)
	if err := tmuxpane.EnsureRunning(id); err != nil {
		fmt.Fprintf(os.Stderr, "stack: tui auto-spawn failed: %v\n", err)
	}
	if *name == store.Main && !*quiet {
		main, _ := s.Get(store.Main)
		announceFocus(main)
	}
	return nil
}
