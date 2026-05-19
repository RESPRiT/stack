// Package tmuxpane auto-spawns a `stack tui` pane in the current tmux
// window when one isn't already running, and can reflow that pane to a
// different split direction when the terminal is resized.
//
// All operations are best-effort: if tmux isn't installed, $TMUX isn't
// set, or any tmux command fails, the caller's primary action (push) is
// never blocked.
package tmuxpane

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Pane sizing — chosen to fit a typical stack (~5 visible topics + chrome)
// without crowding the agent's main pane.
const (
	topRows   = 12
	rightCols = 32
)

// reflowWindow is the coalescing window for leading-edge + trailing
// debounce: the first call fires immediately, then absorbs further calls
// for this long, then fires once more if any landed. Tunes
// drag-responsiveness vs. number of kill/respawns per drag.
const reflowWindow = 60 * time.Millisecond

// lockStale lets us reclaim the reflow lock if a previous holder
// crashed before releasing.
const lockStale = 5 * time.Second

type placement int

const (
	placementTop placement = iota
	placementRight
)

func (p placement) String() string {
	if p == placementRight {
		return "right"
	}
	return "top"
}

func placementFromString(s string) (placement, bool) {
	switch s {
	case "top":
		return placementTop, true
	case "right":
		return placementRight, true
	}
	return placementTop, false
}

func (p placement) splitArgs(target string) []string {
	switch p {
	case placementRight:
		return []string{"split-window", "-h", "-l", strconv.Itoa(rightCols), "-t", target, "-P", "-F", "#{pane_id}"}
	default:
		return []string{"split-window", "-v", "-b", "-l", strconv.Itoa(topRows), "-t", target, "-P", "-F", "#{pane_id}"}
	}
}

// cellAspect normalizes character-cell shape to pixels. Terminal cells
// are roughly twice as tall as they are wide, so a "visually square"
// terminal has cols ≈ 2 × rows. Dividing by this turns the raw cols/rows
// ratio into a visual aspect ratio where 1.0 means visually square.
const cellAspect = 2.0

// Hysteresis thresholds for decide(), in *visual* aspect units
// (cols / (rows × cellAspect)). The asymmetric 0.95..1.05 band around
// 1.0 prevents flapping when a drag-resize crosses the threshold with
// mouse jitter: once placed at top, we don't flip to right until the
// aspect is clearly wide; once at right, we don't flip to top until the
// aspect is clearly tall.
const (
	flipToRight = 1.05
	flipToTop   = 0.95
)

// decide returns the placement we should be in given the window's
// dimensions and where we are now. Uses window dims (not the main
// pane's) because the main pane's aspect depends on where the TUI sits
// — using it would flap on every resize. Normalizes by cellAspect so
// thresholds reflect visual perception rather than raw character grid.
func decide(cols, rows int, current placement) placement {
	if rows == 0 {
		return current
	}
	ratio := float64(cols) / (float64(rows) * cellAspect)
	switch current {
	case placementRight:
		if ratio < flipToTop {
			return placementTop
		}
		return placementRight
	default:
		if ratio >= flipToRight {
			return placementRight
		}
		return placementTop
	}
}

// EnsureResizeHook installs a global client-resized tmux hook that runs
// `stack reflow` when the terminal size changes, if no such hook is
// already present. Safe to call repeatedly: detection is by substring
// match for "<bin> reflow" in the hook list, so we won't double-install.
// Outside tmux this is a no-op.
func EnsureResizeHook() error {
	if !inTmux() {
		return nil
	}
	bin, err := os.Executable()
	if err != nil {
		return err
	}
	marker := bin + " reflow"
	out, err := exec.Command("tmux", "show-hooks", "-g").Output()
	if err != nil {
		return fmt.Errorf("show-hooks: %w", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "client-resized") && strings.Contains(line, marker) {
			return nil
		}
	}
	hookCmd := "run-shell " + shellQuote(marker)
	return exec.Command("tmux", "set-hook", "-ga", "client-resized", hookCmd).Run()
}

// EnsureRunning spawns a TUI pane for sessionID if and only if:
//  1. we're inside tmux ($TMUX is set), AND
//  2. no pane in the current window already has @stack-tui set.
func EnsureRunning(sessionID string) error {
	if !inTmux() {
		return nil
	}
	if exists, err := tuiPaneExists(); err != nil || exists {
		return err
	}
	current, err := currentPaneInfo()
	if err != nil {
		return err
	}
	winCols, winRows, err := windowDims(current.windowID)
	if err != nil {
		return err
	}
	bin, err := os.Executable()
	if err != nil {
		return err
	}
	_, err = spawnAt(current.id, decide(winCols, winRows, placementTop), sessionID, bin)
	return err
}

// Reflow scans every window in the current tmux session for a pane
// marked @stack-tui. For each, it decides the desired placement from the
// largest non-TUI pane in that window; if the current placement differs,
// it kills the TUI pane and respawns it in the new position.
//
// Coalescing uses leading-edge debounce with a trailing call: the first
// caller into a quiet period fires immediately, concurrent callers drop
// their work but raise a trailing flag, and the leading caller performs
// one more reflow at the window's end if any concurrent caller arrived.
// This is snappy on the first event and never leaves the pane stuck
// mid-drag, without flashing the pane on every resize tick.
func Reflow() error {
	if !inTmux() {
		return nil
	}
	acquired, err := acquireReflowLock()
	if err != nil {
		return err
	}
	if !acquired {
		return raiseTrailing()
	}
	defer releaseReflowLock()

	if err := reflowOnce(); err != nil {
		return err
	}
	time.Sleep(reflowWindow)
	pending, err := consumeTrailing()
	if err != nil || !pending {
		return err
	}
	return reflowOnce()
}

func reflowOnce() error {
	tuis, err := findTUIPanes()
	if err != nil {
		return err
	}
	bin, err := os.Executable()
	if err != nil {
		return err
	}
	for _, tui := range tuis {
		// Decide from the window dims, not the main pane's dims. The
		// main pane's aspect ratio depends on where the TUI currently
		// sits (a top-split shrinks rows; a right-split shrinks cols),
		// so using it would flap the placement back and forth on every
		// resize.
		winCols, winRows, err := windowDims(tui.windowID)
		if err != nil {
			continue
		}
		desired := decide(winCols, winRows, currentPlacement(tui))
		if currentPlacement(tui) == desired {
			continue
		}
		if tui.sessionID == "" {
			continue // can't relaunch without an id
		}
		// findMainPane is still the right *target* for the new split,
		// even though it's no longer the source of dimensions.
		main, err := findMainPane(tui.windowID, tui.id)
		if err != nil || main == nil {
			continue
		}
		_ = exec.Command("tmux", "kill-pane", "-t", tui.id).Run()
		if _, err := spawnAt(main.id, desired, tui.sessionID, bin); err != nil {
			return err
		}
	}
	return nil
}

// spawnAt splits target with the given placement, runs `stack tui`
// pinned to sessionID, and stamps the new pane with the user options we
// rely on for detection and recovery.
func spawnAt(target string, p placement, sessionID, bin string) (string, error) {
	args := p.splitArgs(target)
	cmdLine := fmt.Sprintf("CLAUDE_CODE_SESSION_ID=%s exec %s tui", shellQuote(sessionID), shellQuote(bin))
	args = append(args, cmdLine)

	out, err := exec.Command("tmux", args...).Output()
	if err != nil {
		return "", fmt.Errorf("split-window: %w", err)
	}
	paneID := strings.TrimSpace(string(out))
	if paneID == "" {
		return "", errors.New("split-window returned no pane id")
	}
	_ = exec.Command("tmux", "set-option", "-t", paneID, "-p", "@stack-tui", "1").Run()
	_ = exec.Command("tmux", "set-option", "-t", paneID, "-p", "@stack-session", sessionID).Run()
	_ = exec.Command("tmux", "set-option", "-t", paneID, "-p", "@stack-placement", p.String()).Run()
	_ = exec.Command("tmux", "set-option", "-t", paneID, "-p", "remain-on-exit", "on").Run()
	return paneID, nil
}

type paneInfo struct {
	id         string
	windowID   string
	cols       int
	rows       int
	sessionID  string    // value of @stack-session, only set on TUI panes
	placement  placement // value of @stack-placement
	hasPlaceOK bool      // true if @stack-placement was set on this pane
}

func inTmux() bool {
	if os.Getenv("TMUX") == "" {
		return false
	}
	_, err := exec.LookPath("tmux")
	return err == nil
}

// TUIPaneExists is the exported form of tuiPaneExists for callers
// outside this package that want to ask "is there already a TUI pane
// in the current window?" without spawning one.
func TUIPaneExists() (bool, error) { return tuiPaneExists() }

// CloseAll kills every @stack-tui pane in the current tmux session and
// returns how many it closed. No-op outside tmux.
func CloseAll() (int, error) {
	if !inTmux() {
		return 0, nil
	}
	tuis, err := findTUIPanes()
	if err != nil {
		return 0, err
	}
	closed := 0
	for _, tui := range tuis {
		if err := exec.Command("tmux", "kill-pane", "-t", tui.id).Run(); err == nil {
			closed++
		}
	}
	return closed, nil
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

func currentPaneInfo() (paneInfo, error) {
	out, err := exec.Command("tmux", "display-message", "-p", "#{pane_id} #{window_id} #{pane_width} #{pane_height}").Output()
	if err != nil {
		return paneInfo{}, fmt.Errorf("display-message: %w", err)
	}
	return parsePaneLine(strings.TrimSpace(string(out)))
}

// windowDims returns the total window dimensions (the area visible to
// all panes in the window). This is invariant under TUI placement,
// which is what we want when deciding where the TUI should live.
func windowDims(windowID string) (cols, rows int, err error) {
	out, err := exec.Command("tmux", "display-message", "-p", "-t", windowID, "#{window_width} #{window_height}").Output()
	if err != nil {
		return 0, 0, fmt.Errorf("display-message window: %w", err)
	}
	parts := strings.Fields(strings.TrimSpace(string(out)))
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("window dims: unexpected output %q", string(out))
	}
	cols, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, err
	}
	rows, err = strconv.Atoi(parts[1])
	return cols, rows, err
}

// findTUIPanes lists every pane in the current tmux session whose
// @stack-tui option is set, returning enough info to reflow it.
func findTUIPanes() ([]paneInfo, error) {
	out, err := exec.Command("tmux", "list-panes", "-s", "-F",
		"#{pane_id} #{window_id} #{pane_width} #{pane_height} #{@stack-tui} #{@stack-session} #{@stack-placement}").Output()
	if err != nil {
		return nil, fmt.Errorf("list-panes -s: %w", err)
	}
	var panes []paneInfo
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || fields[4] != "1" {
			continue
		}
		cols, _ := strconv.Atoi(fields[2])
		rows, _ := strconv.Atoi(fields[3])
		p := paneInfo{id: fields[0], windowID: fields[1], cols: cols, rows: rows}
		if len(fields) >= 6 {
			p.sessionID = fields[5]
		}
		if len(fields) >= 7 {
			if pl, ok := placementFromString(fields[6]); ok {
				p.placement = pl
				p.hasPlaceOK = true
			}
		}
		panes = append(panes, p)
	}
	return panes, nil
}

// findMainPane picks the largest pane (by area) in window that is not
// the TUI pane itself. Returns nil if there's no other pane.
func findMainPane(windowID, excludeID string) (*paneInfo, error) {
	out, err := exec.Command("tmux", "list-panes", "-t", windowID, "-F",
		"#{pane_id} #{window_id} #{pane_width} #{pane_height}").Output()
	if err != nil {
		return nil, fmt.Errorf("list-panes -t: %w", err)
	}
	var best *paneInfo
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		p, err := parsePaneLine(line)
		if err != nil || p.id == excludeID {
			continue
		}
		if best == nil || p.cols*p.rows > best.cols*best.rows {
			pc := p
			best = &pc
		}
	}
	return best, nil
}

func parsePaneLine(line string) (paneInfo, error) {
	fields := strings.Fields(line)
	if len(fields) < 4 {
		return paneInfo{}, fmt.Errorf("unexpected pane line %q", line)
	}
	cols, err := strconv.Atoi(fields[2])
	if err != nil {
		return paneInfo{}, err
	}
	rows, err := strconv.Atoi(fields[3])
	if err != nil {
		return paneInfo{}, err
	}
	return paneInfo{id: fields[0], windowID: fields[1], cols: cols, rows: rows}, nil
}

// currentPlacement returns the stored @stack-placement opt when present
// (the source of truth, set at spawn). For legacy panes that predate
// the opt, it falls back to inferring from the TUI pane's own shape:
// short-wide = top, tall-narrow = right.
func currentPlacement(tui paneInfo) placement {
	if tui.hasPlaceOK {
		return tui.placement
	}
	if tui.cols > tui.rows {
		return placementTop
	}
	return placementRight
}

// acquireReflowLock attempts to create ~/.stack/reflow.lock exclusively.
// Returns acquired=true if we got it. If a previous holder crashed and
// the lock is older than lockStale, we forcibly reclaim it so the system
// doesn't get stuck.
func acquireReflowLock() (bool, error) {
	path, err := stateFile("reflow.lock")
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err == nil {
		f.Close()
		return true, nil
	}
	if !errors.Is(err, os.ErrExist) {
		return false, err
	}
	info, statErr := os.Stat(path)
	if statErr == nil && time.Since(info.ModTime()) > lockStale {
		_ = os.Remove(path)
		f2, ferr := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if ferr == nil {
			f2.Close()
			return true, nil
		}
	}
	return false, nil
}

func releaseReflowLock() {
	if path, err := stateFile("reflow.lock"); err == nil {
		_ = os.Remove(path)
	}
}

// raiseTrailing marks that a reflow arrived while a leading call held
// the lock. The leading call will check this just before releasing and,
// if set, fire one more reflow.
func raiseTrailing() error {
	path, err := stateFile("reflow.trailing")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	return f.Close()
}

func consumeTrailing() (bool, error) {
	path, err := stateFile("reflow.trailing")
	if err != nil {
		return false, err
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func stateFile(name string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".stack", name), nil
}

// shellQuote wraps s in single quotes for /bin/sh -c, escaping any
// embedded single quotes. tmux runs the spawn command through a shell
// and our sessionID/binary path could contain shell metacharacters.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
