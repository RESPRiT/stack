package main

import (
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"stack/internal/session"
	"stack/internal/store"
)

// cmdHistory implements `stack history [-v] [--all] [-n N]`: the
// session's op log, newest first. By default it lists only pops — the
// session's record of finished topics; --all lists every mutation.
func cmdHistory(args []string) error {
	fs := flag.NewFlagSet("history", flag.ContinueOnError)
	sess := fs.String("session", "", "session id (default: $CLAUDE_CODE_SESSION_ID or newest)")
	all := fs.Bool("all", false, "list every op, not just pops")
	verbose := fs.Bool("v", false, "also print each item's ref")
	limit := fs.Int("n", 20, "show at most N entries (0 = all)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("history takes no positional args (got %q)", fs.Arg(0))
	}
	if *limit < 0 {
		return fmt.Errorf("-n must be >= 0 (got %d)", *limit)
	}
	_, dir, err := session.Resolve(*sess)
	if err != nil {
		if errors.Is(err, session.ErrNoSession) {
			return errors.New("no session to read")
		}
		return err
	}
	entries, err := store.History(dir)
	if err != nil {
		return err
	}
	var shown []store.Entry
	for i := len(entries) - 1; i >= 0; i-- {
		if *limit > 0 && len(shown) == *limit {
			break
		}
		if *all || entries[i].Op == store.OpPop {
			shown = append(shown, entries[i])
		}
	}
	if len(shown) == 0 {
		if *all {
			fmt.Println("(no history)")
		} else {
			fmt.Println("(nothing popped yet)")
		}
		return nil
	}
	now := time.Now()
	ageW, stackW := 0, 0
	for _, e := range shown {
		ageW = max(ageW, len(ago(now, e.At)))
		stackW = max(stackW, len([]rune(entryStacks(e))))
	}
	for i, e := range shown {
		line := fmt.Sprintf("%2d  %-*s  ", i+1, ageW, ago(now, e.At))
		if *all {
			line += fmt.Sprintf("%-6s  ", e.Op)
		}
		stacks := entryStacks(e)
		line += stacks + strings.Repeat(" ", stackW-len([]rune(stacks))) + "  " + entrySubject(e)
		fmt.Println(strings.TrimRight(line, " "))
		if !*verbose || e.Item == nil {
			continue
		}
		if e.Op == store.OpRef && e.From != nil {
			fmt.Printf("      ref: %s → %s\n", refOrNone(e.From.Ref), refOrNone(e.Item.Ref))
		} else if e.Item.Ref != "" {
			fmt.Printf("      ref: %s\n", e.Item.Ref)
		}
	}
	return nil
}

// cmdUndo implements `stack undo [--force] [--quiet]`: revert the
// newest logged op by restoring the state it started from.
func cmdUndo(args []string) error {
	fs := flag.NewFlagSet("undo", flag.ContinueOnError)
	sess := fs.String("session", "", "session id (default: $CLAUDE_CODE_SESSION_ID or newest)")
	force := fs.Bool("force", false, "restore even if the stack changed since the last op")
	quiet := fs.Bool("quiet", false, "skip the context-switch header")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("undo takes no positional args (got %q)", fs.Arg(0))
	}
	id, dir, err := session.Resolve(*sess)
	if err != nil {
		if errors.Is(err, session.ErrNoSession) {
			return errors.New("no session to undo in")
		}
		return err
	}
	cur, err := store.Load(dir)
	if err != nil {
		return err
	}
	e, s, err := store.Undo(dir, *force)
	if err != nil {
		if errors.Is(err, store.ErrDiverged) {
			return fmt.Errorf("stack changed since last op (%s %s ago); --force to restore anyway",
				e.Op, ago(time.Now(), e.At))
		}
		return err
	}
	fmt.Printf("undid %s %s: %s\n", e.Op, undoTarget(id, e), entrySubject(e))
	if e.Op == store.OpRef && e.From != nil {
		fmt.Printf("  ref: %s → %s\n", refOrNone(e.Item.Ref), refOrNone(e.From.Ref))
	} else if e.Item != nil && e.Item.Ref != "" {
		fmt.Printf("  ref: %s\n", e.Item.Ref)
	}
	main, _ := s.Get(store.Main)
	if prev, _ := cur.Get(store.Main); !sameTop(prev, main) && !*quiet {
		announceFocus(main)
	}
	return nil
}

// sameTop reports whether two versions of a stack show the same focus:
// equal depth and an identical top item (topic and ref). Undo announces
// only when this changes, matching push/pop/mv.
func sameTop(a, b []store.Item) bool {
	if len(a) != len(b) {
		return false
	}
	return len(a) == 0 || a[len(a)-1] == b[len(b)-1]
}

func refOrNone(ref string) string {
	if ref == "" {
		return "(none)"
	}
	return ref
}

// undoTarget names where an undone op happened, in the same
// "<id>[/<stack>]" form the mutating commands print.
func undoTarget(id string, e store.Entry) string {
	switch {
	case e.Op == store.OpRm:
		return "of " + targetLabel(id, e.Stack)
	case e.To != "":
		return "from " + targetLabel(id, e.Stack) + " to " + targetLabel(id, e.To)
	default:
		return "on " + targetLabel(id, e.Stack)
	}
}

// entryStacks is the stack column of a history row: the stack name, or
// "from→to" for a cross-stack move.
func entryStacks(e store.Entry) string {
	if e.To != "" {
		return e.Stack + "→" + e.To
	}
	return e.Stack
}

// entrySubject describes what an op acted on: the item's topic, or
// "old → new" for a rename, or the removed stack's size for rm.
func entrySubject(e store.Entry) string {
	if e.Op == store.OpRm {
		if e.Before == nil {
			return ""
		}
		return fmt.Sprintf("(%d item(s))", e.Before.Depth(e.Stack))
	}
	if e.Item == nil {
		return ""
	}
	if e.Op == store.OpRename && e.From != nil {
		return e.From.Topic + " → " + e.Item.Topic
	}
	return e.Item.Topic
}

// ago renders the coarse age of t relative to now: "12s", "4m", "3h",
// "2d".
func ago(now, t time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", max(0, int(d.Seconds())))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
