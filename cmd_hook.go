package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

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
//   - source=clear  → dismiss the panel in the firing Claude Code's
//     tmux window. /clear rolls a NEW session_id (the docs say it
//     preserves; empirically it doesn't as of mid-2026), so matching on
//     session_id breaks. Scoping by window proximity to the firing
//     Claude Code pane gives the right result without disturbing other
//     instances in adjacent windows.
//   - source=resume → retarget the panel to the resumed session id so it
//     shows the right stack. Also scoped to the firing pane's window.
//   - source=startup|compact → no-op. Startup is handled by `stack open`
//     (user-driven), and compact preserves the session so the panel is
//     already correct.
//
// All branches are best-effort and silent on stdout — SessionStart hook
// output is injected as context, and the panel side-effects don't need
// to surface in the conversation. Each fire is logged to ~/.stack/hook.log
// (best-effort, append-only) so SessionStart-driven bugs are diagnosable
// after the fact.
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
		logHookFire("parse-error", "", err.Error(), raw)
		return nil
	}

	var n int
	var actErr error
	switch input.Source {
	case "clear":
		if os.Getenv("TMUX") != "" {
			n, actErr = tmuxpane.CloseInCurrentWindow()
		}
	case "resume":
		if input.SessionID != "" && os.Getenv("TMUX") != "" {
			n, actErr = tmuxpane.RetargetForSession(input.SessionID)
		}
	}
	logHookFire(input.Source, input.SessionID, fmt.Sprintf("n=%d err=%v tmux_pane=%q", n, actErr, os.Getenv("TMUX_PANE")), raw)
	return nil
}

// logHookFire appends a one-line trace to ~/.stack/hook.log so we can
// diagnose whether SessionStart events are reaching us and with what
// payload. Best-effort; silent on failure.
func logHookFire(source, sessionID, extra string, raw []byte) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	path := filepath.Join(home, ".stack", "hook.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s source=%q session_id=%q %s raw=%s\n",
		time.Now().Format(time.RFC3339), source, sessionID, extra, string(raw))
}
