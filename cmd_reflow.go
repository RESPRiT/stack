package main

import (
	"flag"
	"fmt"

	"stack/internal/tmuxpane"
)

func cmdReflow(args []string) error {
	fs := flag.NewFlagSet("reflow", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("reflow takes no positional args (got %q)", fs.Arg(0))
	}
	return tmuxpane.Reflow()
}
