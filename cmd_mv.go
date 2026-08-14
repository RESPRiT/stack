package main

import (
	"errors"
	"flag"
	"fmt"
	"strings"

	"stack/internal/session"
	"stack/internal/store"
)

// cmdMv implements `stack mv [<pos>] [--from <name>] [--to <name>]`:
// move one item between stacks, ref included, landing on the
// destination's top. Both flags default to main, so the two common
// motions are terse: `stack mv --from backlog` promotes a parked item
// to the focus; `stack mv --to backlog` parks the current focus.
func cmdMv(args []string) error {
	fs := flag.NewFlagSet("mv", flag.ContinueOnError)
	sess := fs.String("session", "", "session id (default: $CLAUDE_CODE_SESSION_ID or newest)")
	from := fs.String("from", store.Main, "source stack")
	to := fs.String("to", store.Main, "destination stack (created if new)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	posArg := "top"
	switch fs.NArg() {
	case 0:
	case 1:
		posArg = fs.Arg(0)
	default:
		return fmt.Errorf("mv takes at most one position argument (got %q)", fs.Arg(1))
	}

	id, dir, err := session.Resolve(*sess)
	if err != nil {
		if errors.Is(err, session.ErrNoSession) {
			return errors.New("no session to operate on")
		}
		return err
	}
	cur, err := store.Load(dir)
	if err != nil {
		return err
	}
	topics, ok := cur.Get(*from)
	if !ok {
		return fmt.Errorf("no stack named %q (have: %s)", *from, strings.Join(cur.Names(), ", "))
	}
	if len(topics) == 0 {
		return fmt.Errorf("stack for %s is empty", targetLabel(id, *from))
	}
	pos, err := parsePosition(posArg, len(topics))
	if err != nil {
		return err
	}
	item, s, err := store.MoveItem(dir, *from, pos, *to)
	if err != nil {
		return err
	}
	fmt.Printf("moved from %s to %s (depth %d): %s\n",
		targetLabel(id, *from), targetLabel(id, *to), s.Depth(*to), item.Topic)
	if item.Ref != "" {
		fmt.Printf("  ref: %s\n", item.Ref)
	}
	return nil
}
