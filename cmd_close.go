package main

import (
	"flag"
	"fmt"

	"stack/internal/tmuxpane"
)

func cmdClose(args []string) error {
	fs := flag.NewFlagSet("close", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("close takes no positional args (got %q)", fs.Arg(0))
	}

	n, err := tmuxpane.CloseAll()
	if err != nil {
		return err
	}
	switch n {
	case 0:
		fmt.Println("(no TUI pane open)")
	case 1:
		fmt.Println("closed 1 TUI pane")
	default:
		fmt.Printf("closed %d TUI panes\n", n)
	}
	return nil
}
