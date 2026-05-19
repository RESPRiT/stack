package main

import (
	"flag"
	"fmt"
	"os"

	"stack/internal/session"
	"stack/internal/tmuxpane"
)

func cmdOpen(args []string) error {
	fs := flag.NewFlagSet("open", flag.ContinueOnError)
	sess := fs.String("session", "", "session id (default: $CLAUDE_CODE_SESSION_ID or newest)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("open takes no positional args (got %q)", fs.Arg(0))
	}

	if os.Getenv("TMUX") == "" {
		fmt.Println("(not inside tmux; nothing to open — run `stack tui` in a terminal instead)")
		return nil
	}

	id, _, err := session.ResolveOrCreate(*sess)
	if err != nil {
		return err
	}

	existed, err := tmuxpane.TUIPaneExists()
	if err != nil {
		return err
	}
	if existed {
		fmt.Println("TUI pane already open")
		return nil
	}
	if err := tmuxpane.EnsureRunning(id); err != nil {
		return err
	}
	fmt.Printf("opened TUI pane for %s\n", id)
	return nil
}

