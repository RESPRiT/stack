package main

import (
	"errors"
	"flag"
	"fmt"

	"stack/internal/session"
	"stack/internal/store"
)

// targetLabel names a (session, stack) pair in command output: just the
// session id when targeting main, "<id>/<stack>" otherwise, so the
// default-path output is unchanged from the single-stack days.
func targetLabel(id, name string) string {
	if name == store.Main {
		return id
	}
	return id + "/" + name
}

func cmdPop(args []string) error {
	fs := flag.NewFlagSet("pop", flag.ContinueOnError)
	sess := fs.String("session", "", "session id (default: $CLAUDE_CODE_SESSION_ID or newest)")
	name := fs.String("stack", store.Main, "target stack")
	quiet := fs.Bool("quiet", false, "skip the context-switch header")
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
	popped, after, err := store.Pop(dir, *name)
	if err != nil {
		if errors.Is(err, store.ErrEmpty) {
			return fmt.Errorf("stack for %s is empty", targetLabel(id, *name))
		}
		return err
	}
	fmt.Printf("popped from %s (depth %d): %s\n", targetLabel(id, *name), len(after), popped.Topic)
	if popped.Ref != "" {
		fmt.Printf("  ref: %s\n", popped.Ref)
	}
	if len(after) > 0 {
		fmt.Printf("  next: %s\n", after[len(after)-1].Topic)
	}
	if *name == store.Main && !*quiet {
		announceFocus(after)
	}
	return nil
}
