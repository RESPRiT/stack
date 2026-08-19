// Package tmuxpane auto-spawns a `stack tui` pane in the current tmux
// window when one isn't already running, and reflows that pane —
// placement and width — whenever tmux resizes its window.
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
	rightCols = 64
	// minRightWidth is the floor below which a right-placed pane is
	// unreadable (title row collides with the ID, topics render as a
	// single ellipsis). Sets the lower bound rightWidthFor can return.
	minRightWidth = 16
	// minWindowColsForRight is the smallest window width that admits a
	// right-placed pane. Picked so rightWidthFor returns exactly
	// minRightWidth at this size (winCols/4 == minRightWidth). Below
	// this, decide() forces top placement so the right pane never
	// spawns at an unreadable width — preempts the need for a
	// state-dependent post-spawn downgrade.
	minWindowColsForRight = minRightWidth * 4
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

func (p placement) splitArgs(target string, size int) []string {
	switch p {
	case placementRight:
		return []string{"split-window", "-h", "-l", strconv.Itoa(size), "-t", target, "-P", "-F", "#{pane_id}"}
	default:
		return []string{"split-window", "-v", "-b", "-l", strconv.Itoa(size), "-t", target, "-P", "-F", "#{pane_id}"}
	}
}

// rightWidthFor returns the column count to request for a right-placed
// pane given the current window width. Capped at rightCols so the
// panel doesn't grow indefinitely on wide displays, but stepped down to
// at most 25% of the window so it never dominates a narrow window.
func rightWidthFor(winCols int) int {
	if winCols <= 0 {
		return rightCols
	}
	if cap := winCols / 4; cap > 0 && cap < rightCols {
		return cap
	}
	return rightCols
}

// cellAspect normalizes character-cell shape to pixels. Terminal cells
// are roughly twice as tall as they are wide, so a "visually square"
// terminal has cols ≈ 2 × rows. Dividing by this turns the raw cols/rows
// ratio into a visual aspect ratio where 1.0 means visually square.
const cellAspect = 2.0

// decide returns the placement for a window with the given dimensions.
// Pure function of (cols, rows) — the right placement at a given size
// is always the same, no hysteresis, no path dependence. Uses window
// dims (not the main pane's) because the main pane's aspect depends
// on where the TUI sits — using it would flap on every resize.
// Normalizes by cellAspect so the threshold reflects visual perception
// rather than the raw character grid: visually wide → right,
// visually tall-or-square → top. Windows narrower than
// minWindowColsForRight are forced top so the right pane never spawns
// at an unreadable width.
func decide(cols, rows int) placement {
	if rows == 0 || cols < minWindowColsForRight {
		return placementTop
	}
	if float64(cols) >= float64(rows)*cellAspect {
		return placementRight
	}
	return placementTop
}

// EnsureResizeHook installs a global window-resized tmux hook that runs
// `<bin> tui reflow <window_id>` whenever tmux applies a size change to
// a window: a live client resize of the current window, the deferred
// resize of a background window when it becomes current, or an
// attach-time resize. window-resized is the one event that fires per
// window, after the layout is updated, for every one of those paths —
// so reflow always reads the window's true dims and never skips a
// window it couldn't see.
//
// Safe to call repeatedly: detection is by substring match for
// "<bin> tui reflow" among the window-resized hooks, and any extra
// copies of that hook (window-resized is window-scoped, so it lives in
// `show-hooks -gw`; a lookup in the wrong scope would append a copy per
// TUI start) are unset so exactly one remains. Any client-resized hook
// referencing this binary is unset too: that hook fires once per
// client, before the current window's layout is updated and never for
// background windows, so left in place it would only add a second,
// mistimed reflow. Outside tmux this is a no-op.
func EnsureResizeHook() error {
	if !inTmux() {
		return nil
	}
	bin, err := os.Executable()
	if err != nil {
		return err
	}
	// client-resized is a session hook (-g); window-resized is a window
	// hook (-gw). Concatenate both listings — hook names disambiguate.
	sessionHooks, err := exec.Command("tmux", "show-hooks", "-g").Output()
	if err != nil {
		return fmt.Errorf("show-hooks -g: %w", err)
	}
	windowHooks, err := exec.Command("tmux", "show-hooks", "-gw").Output()
	if err != nil {
		return fmt.Errorf("show-hooks -gw: %w", err)
	}
	state := parseHookState(string(sessionHooks)+string(windowHooks), bin)
	for _, h := range state.stale {
		_ = exec.Command("tmux", "set-hook", "-gu", h).Run()
	}
	for _, h := range state.dupes {
		_ = exec.Command("tmux", "set-hook", "-gwu", h).Run()
	}
	if state.installed {
		return nil
	}
	return exec.Command("tmux", "set-hook", "-gwa", "window-resized", reflowHookCommand(bin)).Run()
}

// reflowHookCommand is the tmux command stored under window-resized.
// run-shell expands formats against the hook's target, so #{window_id}
// names the window whose size just changed. Single-quoted for tmux's
// parser so nothing in the binary path is expanded at parse time.
func reflowHookCommand(bin string) string {
	return "run-shell " + shellQuote(bin+" tui reflow #{window_id}")
}

// hookState is what parseHookState reads out of `tmux show-hooks`.
type hookState struct {
	installed bool     // a window-resized hook already runs "<bin> tui reflow"
	dupes     []string // further window-resized slots running it, e.g. "window-resized[1]"
	stale     []string // client-resized slots referencing bin, e.g. "client-resized[0]"
}

// parseHookState scans show-hooks output (session and window scope
// concatenated) for this binary's reflow hooks. A client-resized line
// counts as stale if it references "<bin> reflow" in either form
// ("<bin> reflow" or "<bin> tui reflow").
func parseHookState(showHooks, bin string) hookState {
	marker := bin + " tui reflow"
	legacyMarker := bin + " reflow"
	var st hookState
	for _, line := range strings.Split(showHooks, "\n") {
		switch {
		case strings.HasPrefix(line, "window-resized"):
			if !strings.Contains(line, marker) {
				continue
			}
			if st.installed {
				if idx := parseHookIndex(line); idx != "" {
					st.dupes = append(st.dupes, "window-resized"+idx)
				}
				continue
			}
			st.installed = true
		case strings.HasPrefix(line, "client-resized"):
			if strings.Contains(line, marker) || strings.Contains(line, legacyMarker) {
				if idx := parseHookIndex(line); idx != "" {
					st.stale = append(st.stale, "client-resized"+idx)
				}
			}
		}
	}
	return st
}

// parseHookIndex returns the "[N]" suffix from a `tmux show-hooks`
// line (e.g. "client-resized[0] run-shell ..."), or "" if absent.
func parseHookIndex(line string) string {
	i := strings.Index(line, "[")
	j := strings.Index(line, "]")
	if i < 0 || j < 0 || j < i {
		return ""
	}
	return line[i : j+1]
}

// EnsureRunning spawns a TUI pane for sessionID if and only if:
//  1. we're inside tmux ($TMUX is set), AND
//  2. no pane in the caller's window already has @stack-tui set.
//
// "The caller's window" is the one holding $TMUX_PANE (see
// callerTarget), so a push from a pane in a background window spawns
// the TUI there, not in whichever window happens to be in front. The
// split target is the largest non-TUI pane in that window — not
// necessarily the active pane. Splitting from a small active pane
// could land the new TUI at a clamped width, breaking the
// dims-determines-size guarantee.
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
	target := current
	if main, err := findMainPane(current.windowID, ""); err == nil && main != nil {
		target = *main
	}
	_, err = spawnAt(target.id, decide(winCols, winRows), winCols, sessionID, bin)
	return err
}

// Reflow repositions and resizes the @stack-tui pane in windowID (a
// tmux window id such as "@12") for that window's current dimensions.
// It is the body of the window-resized hook, so it runs once per applied
// resize of exactly the window that changed; `stack tui reflow` also
// runs it by hand. Windows without a TUI pane return immediately.
//
// Placement and width are both pure functions of the window's dims
// (decide, rightWidthFor). Coalescing splits the work into a fast
// leading path and a settled trailing path so drag-resizing stays
// smooth:
//
//   - Leading and every tick during a drag: reflowOnce handles
//     placement changes only — cheap, no tmux resize-pane calls when
//     placement is stable. tmux's own auto-scaling tracks the drag.
//   - On the first quiet 60 ms window after events stop arriving:
//     enforceCanonicalSize fires once and snaps a right pane to its
//     canonical width. Single resize-pane per drag end, no jitter.
//
// The lock and trailing flag are keyed by window, so concurrent hooks
// for different windows — one terminal resize touches every attached
// client's current window at once — never starve each other.
func Reflow(windowID string) error {
	if !inTmux() {
		return nil
	}
	tuis, err := findTUIPanesInWindow(windowID)
	if err != nil {
		return err
	}
	if len(tuis) == 0 {
		return nil
	}
	acquired, err := acquireReflowLock(windowID)
	if err != nil {
		return err
	}
	if !acquired {
		return raiseTrailing(windowID)
	}
	defer releaseReflowLock(windowID)

	if err := reflowOnce(windowID); err != nil {
		return err
	}
	for {
		time.Sleep(reflowWindow)
		pending, err := consumeTrailing(windowID)
		if err != nil {
			return err
		}
		if !pending {
			break
		}
		// More events landed during sleep — still mid-drag. Handle any
		// placement change but defer size enforcement.
		if err := reflowOnce(windowID); err != nil {
			return err
		}
	}
	return enforceCanonicalSize(windowID)
}

// ReflowAll runs Reflow for every window on the tmux server that holds
// an @stack-tui pane. Manual recovery path (`stack tui reflow --all`);
// the hook never needs it. Returns the number of windows reflowed.
func ReflowAll() (int, error) {
	if !inTmux() {
		return 0, nil
	}
	tuis, err := findTUIPanesScoped(true)
	if err != nil {
		return 0, err
	}
	seen := map[string]bool{}
	for _, tui := range tuis {
		if seen[tui.windowID] {
			continue
		}
		seen[tui.windowID] = true
		if err := Reflow(tui.windowID); err != nil {
			return len(seen) - 1, err
		}
	}
	return len(seen), nil
}

// ResolveWindowID turns any tmux window target (a window id, an index,
// a session:window spec) into its canonical "@N" id. With an empty
// target it resolves the caller's own window (see callerTarget).
func ResolveWindowID(target string) (string, error) {
	args := []string{"display-message", "-p"}
	if target != "" {
		args = append(args, "-t", target)
	} else {
		args = append(args, callerTarget()...)
	}
	args = append(args, "#{window_id}")
	out, err := exec.Command("tmux", args...).Output()
	if err != nil {
		return "", fmt.Errorf("display-message window: %w", err)
	}
	id := strings.TrimSpace(string(out))
	if id == "" {
		return "", fmt.Errorf("no tmux window for target %q", target)
	}
	return id, nil
}

func reflowOnce(windowID string) error {
	tuis, err := findTUIPanesInWindow(windowID)
	if err != nil {
		return err
	}
	bin, err := os.Executable()
	if err != nil {
		return err
	}
	// Decide from the window dims, not the main pane's dims. The main
	// pane's aspect ratio depends on where the TUI currently sits (a
	// top-split shrinks rows; a right-split shrinks cols), so using it
	// would flap the placement back and forth on every resize.
	winCols, winRows, err := windowDims(windowID)
	if err != nil {
		return err
	}
	desired := decide(winCols, winRows)
	for _, tui := range tuis {
		if currentPlacement(tui) == desired {
			continue
		}
		if tui.sessionID == "" {
			continue // can't relaunch without an id
		}
		// findMainPane is still the right *target* for the new split,
		// even though it's no longer the source of dimensions.
		main, err := findMainPane(windowID, tui.id)
		if err != nil || main == nil {
			continue
		}
		_ = exec.Command("tmux", "kill-pane", "-t", tui.id).Run()
		if _, err := spawnAt(main.id, desired, winCols, tui.sessionID, bin); err != nil {
			return err
		}
	}
	return nil
}

// enforceCanonicalSize snaps a right-placed @stack-tui pane in windowID
// to rightWidthFor(winCols). Invoked by Reflow only after events
// settle, so a drag-resize doesn't trigger a tmux resize-pane on every
// tick — the user sees tmux's own auto-scaling track the drag smoothly,
// and the canonical width snap fires once at the end. Top panes are
// left alone (their height is stack-driven by maybeResize inside the
// TUI process).
func enforceCanonicalSize(windowID string) error {
	tuis, err := findTUIPanesInWindow(windowID)
	if err != nil {
		return err
	}
	winCols, _, err := windowDims(windowID)
	if err != nil {
		return err
	}
	target := rightWidthFor(winCols)
	for _, tui := range tuis {
		if currentPlacement(tui) != placementRight || tui.cols == target {
			continue
		}
		_ = exec.Command("tmux", "resize-pane", "-t", tui.id, "-x", strconv.Itoa(target)).Run()
	}
	return nil
}

// spawnAt splits target with the given placement, runs `stack tui`
// pinned to sessionID, and stamps the new pane with the user options
// we rely on for detection and recovery. winCols sizes a right
// placement against the 25 % cap (see rightWidthFor); pass 0 to fall
// back to rightCols. decide() guarantees winCols ≥ minWindowColsForRight
// before returning placementRight, so the resulting right pane is
// always ≥ minRightWidth — no post-spawn fallback needed.
func spawnAt(target string, p placement, winCols int, sessionID, bin string) (string, error) {
	size := topRows
	if p == placementRight {
		size = rightWidthFor(winCols)
	}
	args := p.splitArgs(target, size)
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
	// remain-on-exit keeps the pane around after the TUI process dies
	// so a crash log stays visible. Useful during development, noisy
	// in normal use (every `q` leaves a "Pane is dead" stub). Gate on
	// STACK_DEBUG so the default UX is the clean one.
	if os.Getenv("STACK_DEBUG") != "" {
		_ = exec.Command("tmux", "set-option", "-t", paneID, "-p", "remain-on-exit", "on").Run()
	}
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
// in the caller's window?" without spawning one.
func TUIPaneExists() (bool, error) { return tuiPaneExists() }

// IsTUIPane reports whether the given pane has @stack-tui set. Used
// by `stack tui`'s auto-detect: when invoked inside a marked pane it
// runs the foreground Bubble Tea process; outside, it spawns the pane.
func IsTUIPane(paneID string) bool {
	out, err := exec.Command("tmux", "show-option", "-t", paneID, "-p", "-v", "@stack-tui").Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "1"
}

// TUIPaneInfo is the exported view of a discovered @stack-tui pane.
type TUIPaneInfo struct {
	ID        string // tmux pane id, e.g. "%264"
	SessionID string // value of the pane's @stack-session option
}

// AllTUIPanes returns every @stack-tui pane on the tmux server —
// across every session, not just the current one. HMR uses this so a
// single daemon keeps every running TUI in sync after a rebuild.
func AllTUIPanes() ([]TUIPaneInfo, error) {
	tuis, err := findTUIPanesScoped(true)
	if err != nil {
		return nil, err
	}
	out := make([]TUIPaneInfo, 0, len(tuis))
	for _, t := range tuis {
		out = append(out, TUIPaneInfo{ID: t.id, SessionID: t.sessionID})
	}
	return out, nil
}

// AllHMROptedInPanes returns @stack-tui panes whose @stack-hmr option
// is "1" — i.e., panes the user has explicitly opted into HMR via the
// TUI's `h` toggle. The HMR daemon respawns only these on a rebuild
// so iterating in one project doesn't flicker a stack TUI in another.
func AllHMROptedInPanes() ([]TUIPaneInfo, error) {
	all, err := AllTUIPanes()
	if err != nil {
		return nil, err
	}
	out := make([]TUIPaneInfo, 0, len(all))
	for _, p := range all {
		if IsHMROptedIn(p.ID) {
			out = append(out, p)
		}
	}
	return out, nil
}

// IsHMROptedIn reports whether the given pane has @stack-hmr set to
// "1". Best-effort: any tmux error or non-"1" value reads as opted-out.
func IsHMROptedIn(paneID string) bool {
	out, err := exec.Command("tmux", "show-option", "-t", paneID, "-p", "-v", "@stack-hmr").Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "1"
}

// SetHMROptIn sets or unsets @stack-hmr on the given pane.
func SetHMROptIn(paneID string, on bool) error {
	val := "0"
	if on {
		val = "1"
	}
	return exec.Command("tmux", "set-option", "-t", paneID, "-p", "@stack-hmr", val).Run()
}

// AnyHMROptedIn is a convenience for the auto-stop lifecycle: returns
// true iff at least one @stack-tui pane anywhere on the tmux server
// has @stack-hmr=1.
func AnyHMROptedIn() bool {
	panes, err := AllHMROptedInPanes()
	if err != nil {
		return false
	}
	return len(panes) > 0
}

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

// CloseInCurrentWindow kills @stack-tui panes that live in the same
// tmux window as the caller (resolved via $TMUX_PANE → window). Used
// by the SessionStart=clear hook: matching on session_id breaks
// because Claude Code rolls a new session_id on /clear, so we scope by
// window-proximity to the firing Claude Code pane instead. Panes
// belonging to other Claude Code instances (different windows) are
// left untouched. If $TMUX_PANE isn't set, falls back to closing every
// @stack-tui pane in the current tmux session.
func CloseInCurrentWindow() (int, error) {
	if !inTmux() {
		return 0, nil
	}
	myWindow, err := currentWindowID()
	if err != nil {
		return 0, err
	}
	tuis, err := findTUIPanes()
	if err != nil {
		return 0, err
	}
	closed := 0
	for _, tui := range tuis {
		if myWindow != "" && tui.windowID != myWindow {
			continue
		}
		if err := exec.Command("tmux", "kill-pane", "-t", tui.id).Run(); err == nil {
			closed++
		}
	}
	return closed, nil
}

// currentWindowID returns the tmux window id containing the caller's
// pane (preferring $TMUX_PANE, falling back to the active pane).
func currentWindowID() (string, error) { return ResolveWindowID("") }

// RetargetForSession retargets any @stack-tui pane in the current tmux
// session whose @stack-session differs from newSessionID: the old pane
// is killed and replaced in the same window with a fresh pane bound to
// newSessionID. Used by the SessionStart=resume hook so the panel
// follows a /resume into a different Claude Code session.
//
// Panes already matching newSessionID are left alone. If no @stack-tui
// pane exists, this is a no-op — we don't spawn a pane the user didn't
// already have open.
func RetargetForSession(newSessionID string) (int, error) {
	if !inTmux() || newSessionID == "" {
		return 0, nil
	}
	myWindow, _ := currentWindowID()
	tuis, err := findTUIPanes()
	if err != nil {
		return 0, err
	}
	bin, err := os.Executable()
	if err != nil {
		return 0, err
	}
	retargeted := 0
	for _, tui := range tuis {
		if myWindow != "" && tui.windowID != myWindow {
			continue
		}
		if tui.sessionID == newSessionID {
			continue
		}
		main, err := findMainPane(tui.windowID, tui.id)
		if err != nil || main == nil {
			continue
		}
		_ = exec.Command("tmux", "kill-pane", "-t", tui.id).Run()
		winCols, winRows, werr := windowDims(tui.windowID)
		if werr != nil {
			continue
		}
		p := decide(winCols, winRows)
		if _, err := spawnAt(main.id, p, winCols, newSessionID, bin); err != nil {
			continue
		}
		retargeted++
	}
	return retargeted, nil
}

// callerTarget returns the tmux -t arguments that scope a query to the
// caller's own pane: $TMUX_PANE, which tmux sets in every pane's
// environment. Without it (a run-shell job, a scrubbed environment)
// there is no way to name the caller's pane and the query runs
// target-less.
//
// The scoping matters because of how tmux resolves a target-less
// command from an unattached client: it uses the pane the command runs
// in only to pick the session, then answers for that session's
// *current* window and active pane. A target-less query from a pane in
// a background window therefore describes the foreground window.
func callerTarget() []string {
	if pane := os.Getenv("TMUX_PANE"); pane != "" {
		return []string{"-t", pane}
	}
	return nil
}

// tuiPaneExists reports whether the caller's window already holds an
// @stack-tui pane. `list-panes -t <pane>` lists that pane's window.
func tuiPaneExists() (bool, error) {
	args := append([]string{"list-panes"}, callerTarget()...)
	args = append(args, "-F", "#{@stack-tui}")
	out, err := exec.Command("tmux", args...).Output()
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

// currentPaneInfo describes the caller's own pane (see callerTarget).
func currentPaneInfo() (paneInfo, error) {
	args := append([]string{"display-message", "-p"}, callerTarget()...)
	args = append(args, "#{pane_id} #{window_id} #{pane_width} #{pane_height}")
	out, err := exec.Command("tmux", args...).Output()
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
// @stack-tui option is set. For all-server discovery (used by HMR),
// see findTUIPanesScoped.
func findTUIPanes() ([]paneInfo, error) {
	return findTUIPanesScoped(false)
}

// findTUIPanesScoped lists @stack-tui panes either in the current tmux
// session (allSessions=false) or across every session on the server
// (allSessions=true).
func findTUIPanesScoped(allSessions bool) ([]paneInfo, error) {
	if allSessions {
		return listTUIPanes("-a") // every session on this tmux server
	}
	return listTUIPanes("-s") // current session only
}

// findTUIPanesInWindow lists the @stack-tui panes in one window.
func findTUIPanesInWindow(windowID string) ([]paneInfo, error) {
	return listTUIPanes("-t", windowID)
}

// listTUIPanes runs `tmux list-panes <scope...>` and keeps the panes
// whose @stack-tui option is set.
func listTUIPanes(scope ...string) ([]paneInfo, error) {
	args := append([]string{"list-panes"}, scope...)
	args = append(args, "-F",
		"#{pane_id} #{window_id} #{pane_width} #{pane_height} #{@stack-tui} #{@stack-session} #{@stack-placement}")
	out, err := exec.Command("tmux", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("list-panes %s: %w", strings.Join(scope, " "), err)
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

// reflowStateName is the ~/.stack file name for one window's reflow
// lock or trailing flag: "reflow-12.lock" for window "@12". Keyed per
// window so concurrent reflows of different windows don't contend.
func reflowStateName(windowID, kind string) string {
	return "reflow-" + strings.TrimPrefix(windowID, "@") + "." + kind
}

// acquireReflowLock attempts to create the window's lock file
// exclusively. Returns acquired=true if we got it. If a previous holder
// crashed and the lock is older than lockStale, we forcibly reclaim it
// so the window doesn't get stuck.
func acquireReflowLock(windowID string) (bool, error) {
	path, err := stateFile(reflowStateName(windowID, "lock"))
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

func releaseReflowLock(windowID string) {
	if path, err := stateFile(reflowStateName(windowID, "lock")); err == nil {
		_ = os.Remove(path)
	}
}

// raiseTrailing marks that a reflow for this window arrived while a
// leading call held its lock. The leading call checks this just before
// releasing and, if set, fires one more reflow.
func raiseTrailing(windowID string) error {
	path, err := stateFile(reflowStateName(windowID, "trailing"))
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	return f.Close()
}

func consumeTrailing(windowID string) (bool, error) {
	path, err := stateFile(reflowStateName(windowID, "trailing"))
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
