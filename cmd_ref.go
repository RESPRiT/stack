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
)

// cmdRef implements `stack ref <pos> [...args]`.
//
//   - `stack ref <pos>`         → print the ref.
//   - `stack ref <pos> <text>`  → set inline (multiple args joined with spaces).
//   - `stack ref <pos> -`       → read ref from stdin.
//   - `stack ref <pos> --clear` → remove the ref.
func cmdRef(args []string) error {
	fs := flag.NewFlagSet("ref", flag.ContinueOnError)
	sess := fs.String("session", "", "session id (default: $CLAUDE_CODE_SESSION_ID or newest)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	// --clear is documented to come *after* the position
	// (`stack ref top --clear`), so we scan the positional args for it
	// rather than relying on flag.FlagSet (which stops parsing at the
	// first non-flag and would miss it).
	clearFlag := false
	cleaned := rest[:0]
	for _, a := range rest {
		if a == "--clear" {
			clearFlag = true
			continue
		}
		cleaned = append(cleaned, a)
	}
	rest = cleaned
	if len(rest) < 1 {
		return errors.New("ref requires a position (top, bottom, or 1-based)")
	}
	posArg := rest[0]
	textParts := rest[1:]

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
	if len(cur.Topics) == 0 {
		return fmt.Errorf("stack for %s is empty", id)
	}
	pos, err := parsePosition(posArg, len(cur.Topics))
	if err != nil {
		return err
	}

	// Resolve the mode. --clear wins outright; an explicit '-' arg
	// triggers stdin; otherwise positional text sets inline; otherwise
	// (no text at all) print the current ref.
	switch {
	case clearFlag:
		if len(textParts) > 0 {
			return errors.New("--clear takes no text argument")
		}
		if _, err := store.SetRef(dir, pos, ""); err != nil {
			return err
		}
		fmt.Printf("cleared ref at position %d in %s\n", pos, id)
		return nil

	case len(textParts) == 1 && textParts[0] == "-":
		raw, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
		// Trim only trailing newlines — interior whitespace is the
		// user's content.
		text := strings.TrimRight(string(raw), "\n")
		if _, err := store.SetRef(dir, pos, text); err != nil {
			return err
		}
		fmt.Printf("set ref at position %d in %s (%d bytes from stdin)\n", pos, id, len(text))
		return nil

	case len(textParts) > 0:
		text := strings.TrimSpace(strings.Join(textParts, " "))
		if text == "" {
			return errors.New("ref text cannot be only whitespace; use --clear to remove")
		}
		if _, err := store.SetRef(dir, pos, text); err != nil {
			return err
		}
		fmt.Printf("set ref at position %d in %s\n", pos, id)
		return nil

	default:
		// No text → print mode.
		idx := len(cur.Topics) - pos
		fmt.Print(cur.Topics[idx].Ref)
		if cur.Topics[idx].Ref != "" && !strings.HasSuffix(cur.Topics[idx].Ref, "\n") {
			fmt.Println()
		}
		return nil
	}
}
