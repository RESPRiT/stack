package main

import (
	"errors"
	"flag"
	"fmt"
	"strings"

	"stack/internal/banner"
	"stack/internal/session"
	"stack/internal/store"
)

// cmdFocus implements `stack focus`: print the markdown context-switch
// header naming whatever now sits on top of a stack.
//
// It reads state and never mutates it. push, pop, and mv print the same
// header (via announceFocus) when they move main's top; this command is
// the on-demand form an agent runs to re-orient after a /clear or when
// asked what it's working on.
func cmdFocus(args []string) error {
	fs := flag.NewFlagSet("focus", flag.ContinueOnError)
	sess := fs.String("session", "", "session id (default: $CLAUDE_CODE_SESSION_ID or newest)")
	name := fs.String("stack", store.Main, "stack whose top to announce")
	width := fs.Int("width", 0, "rule width in columns (default: measure the tmux pane)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("focus takes no positional args (got %q)", fs.Arg(0))
	}

	_, dir, err := session.Resolve(*sess)
	if err != nil {
		// No session yet is an empty focus, not a failure: the caller
		// chained this onto something and still wants a header back.
		if errors.Is(err, session.ErrNoSession) {
			fmt.Println(banner.Render(banner.Focus{Stack: *name}, *width))
			return nil
		}
		return err
	}
	s, err := store.Load(dir)
	if err != nil {
		return err
	}
	topics, ok := s.Get(*name)
	if !ok {
		return fmt.Errorf("no stack named %q (have: %s)", *name, strings.Join(s.Names(), ", "))
	}

	f := banner.Focus{Stack: *name, Depth: len(topics)}
	if len(topics) > 0 {
		top := topics[len(topics)-1]
		f.Topic, f.Ref = top.Topic, top.Ref
	}
	fmt.Println(banner.Render(f, *width))
	return nil
}
