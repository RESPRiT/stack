package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"stack/internal/tmuxpane"
)

// hookInput is the SessionStart payload Claude Code writes to stdin.
// Only the fields stack reads are declared; extras are ignored.
type hookInput struct {
	SessionID string `json:"session_id"`
	Source    string `json:"source"`
}

func cmdHook(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("hook requires a subcommand (e.g. session-start)")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "session-start":
		return cmdHookSessionStart(rest)
	default:
		return fmt.Errorf("unknown hook subcommand %q", sub)
	}
}

// cmdHookSessionStart reacts to Claude Code's SessionStart event so the
// stack panel stays consistent with the user's session state:
//
//   - source=clear  → dismiss the panel for this session id. /clear is
//     a logical reset; leaving a stale pane around defeats the point.
//   - source=resume → retarget the panel to the resumed session id so it
//     shows the right stack.
//   - source=startup|compact → no-op. Startup is handled by `stack open`
//     (user-driven), and compact preserves the session so the panel is
//     already correct.
//
// All branches are best-effort and silent on stdout — SessionStart hook
// output is injected as context, and the panel side-effects don't need
// to surface in the conversation.
func cmdHookSessionStart(args []string) error {
	fs := flag.NewFlagSet("hook session-start", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}

	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil
	}
	var input hookInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil
	}
	if input.SessionID == "" {
		return nil
	}
	if os.Getenv("TMUX") == "" {
		return nil
	}

	switch input.Source {
	case "clear":
		_, _ = tmuxpane.CloseForSession(input.SessionID)
	case "resume":
		_, _ = tmuxpane.RetargetForSession(input.SessionID)
	}
	return nil
}
