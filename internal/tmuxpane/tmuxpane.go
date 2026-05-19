// Package tmuxpane auto-spawns a `stack tui` pane in the current tmux
// window when one isn't already running, and decides whether to split
// to the right or above based on the active pane's aspect ratio.
//
// All operations are best-effort: if tmux isn't installed, $TMUX isn't
// set, or any tmux command fails, EnsureRunning returns nil so a push
// is never blocked by display-side issues.
package tmuxpane

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// EnsureRunning spawns a TUI pane for sessionID if and only if:
//  1. we're inside tmux ($TMUX is set), AND
//  2. no pane in the current window already has @stack-tui set.
//
// The spawned pane gets pinned to sessionID via CLAUDE_CODE_SESSION_ID
// so it doesn't drift if the user later opens other sessions.
func EnsureRunning(sessionID string) error {
	if os.Getenv("TMUX") == "" {
		return nil
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		return nil
	}
	if exists, err := tuiPaneExists(); err != nil || exists {
		return err
	}

	cols, rows, err := currentPaneDims()
	if err != nil {
		return err
	}
	bin, err := os.Executable()
	if err != nil {
		return err
	}

	// width >= 1.5 * height → split right (32 cols); else above (16 rows).
	// Char cells are taller than wide, so the threshold biases toward
	// "top" for square-ish panes where the agent needs the cols.
	args := splitArgs(cols, rows)
	cmdLine := fmt.Sprintf("CLAUDE_CODE_SESSION_ID=%s exec %s tui", shellQuote(sessionID), shellQuote(bin))
	args = append(args, "-P", "-F", "#{pane_id}", cmdLine)

	out, err := exec.Command("tmux", args...).Output()
	if err != nil {
		return fmt.Errorf("split-window: %w", err)
	}
	paneID := strings.TrimSpace(string(out))
	if paneID == "" {
		return nil
	}

	// remain-on-exit keeps the pane around if the TUI dies so the user
	// can read whatever it printed before crashing.
	_ = exec.Command("tmux", "set-option", "-t", paneID, "-p", "@stack-tui", "1").Run()
	_ = exec.Command("tmux", "set-option", "-t", paneID, "-p", "remain-on-exit", "on").Run()
	return nil
}

// Pane sizing — chosen to fit a typical stack (~5 visible topics + chrome)
// without crowding the agent's main pane.
const (
	topRows   = 12
	rightCols = 32
)

func splitArgs(cols, rows int) []string {
	if cols*2 >= rows*3 { // cols >= 1.5 * rows
		return []string{"split-window", "-h", "-l", strconv.Itoa(rightCols)}
	}
	return []string{"split-window", "-v", "-b", "-l", strconv.Itoa(topRows)}
}

func tuiPaneExists() (bool, error) {
	out, err := exec.Command("tmux", "list-panes", "-F", "#{@stack-tui}").Output()
	if err != nil {
		return false, fmt.Errorf("list-panes: %w", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(line) == "1" {
			return true, nil
		}
	}
	return false, nil
}

func currentPaneDims() (cols, rows int, err error) {
	out, err := exec.Command("tmux", "display-message", "-p", "#{pane_width} #{pane_height}").Output()
	if err != nil {
		return 0, 0, fmt.Errorf("display-message: %w", err)
	}
	parts := strings.Fields(strings.TrimSpace(string(out)))
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("display-message: unexpected output %q", string(out))
	}
	cols, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, err
	}
	rows, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, err
	}
	return cols, rows, nil
}

// shellQuote wraps s in single quotes for /bin/sh -c, escaping any
// embedded single quotes. Used because tmux runs the spawn command
// through a shell and our sessionID/binary path could (in principle)
// contain shell metacharacters.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
