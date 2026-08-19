package main

import (
	"flag"
	"fmt"

	"stack/internal/tmuxpane"
)

// cmdReflow is `stack tui reflow [<window>] [--all]`. With no argument
// it reflows the caller's own tmux window; with a window target (an
// "@N" id, index, or session:window spec) it reflows that window; with
// --all it reflows every window on the server that holds a TUI pane.
// The window-resized hook installed by the TUI invokes the one-window
// form with the window that just changed size.
func cmdReflow(args []string) error {
	fs := flag.NewFlagSet("reflow", flag.ContinueOnError)
	all := fs.Bool("all", false, "reflow every window on the tmux server that has a TUI pane")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return fmt.Errorf("reflow takes at most one window target (got %q)", fs.Args())
	}
	if *all {
		if fs.NArg() > 0 {
			return fmt.Errorf("reflow --all takes no window target (got %q)", fs.Arg(0))
		}
		n, err := tmuxpane.ReflowAll()
		if err != nil {
			return err
		}
		fmt.Printf("reflowed %d window(s)\n", n)
		return nil
	}
	windowID, err := tmuxpane.ResolveWindowID(fs.Arg(0))
	if err != nil {
		return err
	}
	return tmuxpane.Reflow(windowID)
}
