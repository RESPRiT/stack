package main

import (
	"errors"
	"flag"
	"fmt"
	"strconv"
	"strings"

	"stack/internal/session"
	"stack/internal/store"
)

func cmdEdit(args []string) error {
	if len(args) == 0 {
		return errors.New("edit requires a subcommand: rename | move")
	}
	op, rest := args[0], args[1:]
	switch op {
	case "rename":
		return cmdEditRename(rest)
	case "move":
		return cmdEditMove(rest)
	default:
		return fmt.Errorf("unknown edit op %q (use rename or move)", op)
	}
}

func cmdEditRename(args []string) error {
	fs := flag.NewFlagSet("edit rename", flag.ContinueOnError)
	sess := fs.String("session", "", "session id (default: $CLAUDE_CODE_SESSION_ID or newest)")
	name := fs.String("stack", store.Main, "target stack")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 2 {
		return errors.New("edit rename requires: <position> <new-text>")
	}
	posArg, textParts := rest[0], rest[1:]
	newText := strings.TrimSpace(strings.Join(textParts, " "))
	if newText == "" {
		return errors.New("new-text cannot be empty")
	}

	id, dir, err := session.Resolve(*sess)
	if err != nil {
		if errors.Is(err, session.ErrNoSession) {
			return errors.New("no session to edit")
		}
		return err
	}
	topics, label, err := editTarget(dir, id, *name)
	if err != nil {
		return err
	}
	pos, err := parsePosition(posArg, len(topics))
	if err != nil {
		return err
	}
	if _, err := store.Rename(dir, *name, pos, newText); err != nil {
		return err
	}
	fmt.Printf("renamed position %d in %s: %s\n", pos, label, newText)
	return nil
}

// editTarget loads the session and resolves the named stack for an edit
// op, translating "unknown stack" and "empty stack" into the friendly
// errors every edit-family command shares.
func editTarget(dir, id, name string) ([]store.Item, string, error) {
	cur, err := store.Load(dir)
	if err != nil {
		return nil, "", err
	}
	topics, ok := cur.Get(name)
	if !ok {
		return nil, "", fmt.Errorf("no stack named %q (have: %s)", name, strings.Join(cur.Names(), ", "))
	}
	label := targetLabel(id, name)
	if len(topics) == 0 {
		return nil, "", fmt.Errorf("stack for %s is empty", label)
	}
	return topics, label, nil
}

func cmdEditMove(args []string) error {
	fs := flag.NewFlagSet("edit move", flag.ContinueOnError)
	sess := fs.String("session", "", "session id (default: $CLAUDE_CODE_SESSION_ID or newest)")
	name := fs.String("stack", store.Main, "target stack")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) != 2 {
		return errors.New("edit move requires: <from> <to>")
	}
	id, dir, err := session.Resolve(*sess)
	if err != nil {
		if errors.Is(err, session.ErrNoSession) {
			return errors.New("no session to edit")
		}
		return err
	}
	topics, label, err := editTarget(dir, id, *name)
	if err != nil {
		return err
	}
	from, err := parsePosition(rest[0], len(topics))
	if err != nil {
		return err
	}
	to, err := parsePosition(rest[1], len(topics))
	if err != nil {
		return err
	}
	if from == to {
		fmt.Printf("no-op: position %d already at target\n", from)
		return nil
	}
	if _, err := store.Move(dir, *name, from, to); err != nil {
		return err
	}
	fmt.Printf("moved position %d → %d in %s\n", from, to, label)
	return nil
}

// parsePosition turns "top", "bottom", or a numeric string into a
// 1-based top-first position validated against stackLen. Numeric inputs
// outside 1..stackLen are rejected with a friendly range hint.
func parsePosition(s string, stackLen int) (int, error) {
	switch s {
	case "top":
		return 1, nil
	case "bottom":
		return stackLen, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("invalid position %q (use top, bottom, or 1..%d)", s, stackLen)
	}
	if n < 1 || n > stackLen {
		return 0, fmt.Errorf("position %d out of range (stack has %d item(s))", n, stackLen)
	}
	return n, nil
}
