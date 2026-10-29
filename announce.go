package main

import (
	"flag"
	"fmt"
	"os"

	"stack/internal/banner"
	"stack/internal/store"
)

// agentTrailer follows the header when an agent is the caller. It lives
// in stdout rather than only in the skill prompts because the header
// must reach the reply even when the agent runs push/pop/mv as bare
// Bash with no skill loaded, and because agents tend to assume the user
// can see tool output, which they can't.
const agentTrailer = "↑ The user does not see tool output. Echo the header above verbatim at the top of this turn's final reply (not an interim note), with your orientation beneath it. If the focus moves again this turn, only the last header counts."

// announceFocus prints the context-switch header for main's top after a
// command that moved it. topics is main's post-mutation contents, so no
// reload is needed.
func announceFocus(topics []store.Item) {
	f := banner.Focus{Stack: store.Main, Depth: len(topics)}
	if len(topics) > 0 {
		top := topics[len(topics)-1]
		f.Topic, f.Ref = top.Topic, top.Ref
	}
	fmt.Println()
	fmt.Println(banner.Render(f, 0))
	if agentCaller() {
		fmt.Println()
		fmt.Println(agentTrailer)
	}
}

// agentCaller reports whether stdout is headed into a Claude Code tool
// result: the session env var is set and stdout isn't a terminal. A
// human running stack in a shell gets the header without the trailer.
func agentCaller() bool {
	if os.Getenv("CLAUDE_CODE_SESSION_ID") == "" {
		return false
	}
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice == 0
}

// parseInterspersed parses fs over args, allowing flags to follow
// positional arguments (`stack mv top --from backlog`). The standard
// flag package stops at the first positional; this re-parses the
// remainder after each one and returns the positionals in order.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}
