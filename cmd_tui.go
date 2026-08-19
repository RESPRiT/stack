package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/fsnotify/fsnotify"

	"stack/internal/session"
	"stack/internal/store"
	"stack/internal/tmuxpane"
)

// eventBuf is the channel depth between the watcher goroutine and the
// Bubble Tea Cmd loop. 16 is plenty: each push generates at most a few
// fsnotify ops, and the consumer drains one per Update tick.
const eventBuf = 16

// Scroll animation timings. Horizontal and vertical modes share the
// initial stable hold; the per-step rate and end-hold differ. Modes are
// mutually exclusive (see currentScrollMode).
const (
	// scrollStable is the initial hold at offset 0 before scrolling begins.
	scrollStable = 10 * time.Second
	// scrollCharsPerSec controls horizontal-mode glide rate.
	scrollCharsPerSec = 8
	// scrollEndPad is the rune-width of breathing room appended past an
	// over-width topic for the horizontal-scroll cycle. Two of those slots
	// land as visible trailing spaces before the right edge; the third
	// compensates for the head "…" that consumes one rendered slot.
	scrollEndPad = 3
	// scrollHoldEnd is the pause after horizontal scrolling reaches the
	// end before the cycle resets to offset 0.
	scrollHoldEnd = 2 * time.Second
	// scrollVStep advances the vertical view by one row every this often.
	scrollVStep = 1 * time.Second
	// scrollVHold is the pause at the bottom of the list before the
	// vertical cycle restarts.
	scrollVHold = 2 * time.Second
	// tickInterval drives the scroll animation. 5 chars/sec implies a
	// 200 ms tick keeps the motion smooth (one char per tick).
	tickInterval = 200 * time.Millisecond

	// View() chrome + tail gap. The tail gap is the user-visible 1-row
	// space between the footer and the panel divider; chrome is title +
	// blank + blank + footer.
	chromeAndGapRows = 5
)

type scrollMode int

const (
	scrollNone scrollMode = iota
	scrollHorizontal
	scrollVertical
)

// cmdTUI dispatches the TUI command family. Subcommands manage the
// @stack-tui pane lifecycle:
//
//   - `stack tui open`    spawn the pane (no-op if one already exists).
//   - `stack tui close`   dismiss any pane in the current tmux session.
//   - `stack tui reflow`  reposition/resize the pane for its window's dims.
//
// `stack tui` (no sub) auto-detects: if invoked inside an @stack-tui
// pane (or outside tmux entirely), it runs the foreground Bubble Tea
// process; otherwise it falls through to `tui open` and spawns the
// pane. tmux's split-window invokes `<bin> tui` for the spawned pane,
// so the auto-detect lands on the foreground path inside the pane.
func cmdTUI(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "open":
			return cmdOpen(args[1:])
		case "close":
			return cmdClose(args[1:])
		case "reflow":
			return cmdReflow(args[1:])
		}
	}
	// No sub (or an unrecognized first arg that's actually a flag like
	// `--session=foo`). Auto-detect whether to run the foreground TUI
	// or spawn the pane.
	if shouldRunForegroundTUI() {
		return runForegroundTUI(args)
	}
	return cmdOpen(args)
}

// shouldRunForegroundTUI is true when `stack tui` should take over the
// current terminal with the Bubble Tea program rather than spawning a
// tmux pane: outside tmux entirely, or inside an existing @stack-tui
// pane (the case during tmux's split-window invocation).
func shouldRunForegroundTUI() bool {
	if os.Getenv("TMUX") == "" {
		return true
	}
	paneID := os.Getenv("TMUX_PANE")
	if paneID == "" {
		return true
	}
	return tmuxpane.IsTUIPane(paneID)
}

func runForegroundTUI(args []string) error {
	fs := flag.NewFlagSet("tui", flag.ContinueOnError)
	sess := fs.String("session", "", "session id (default: $CLAUDE_CODE_SESSION_ID or newest)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	id, dir, err := session.Resolve(*sess)
	if err != nil && !errors.Is(err, session.ErrNoSession) {
		return err
	}
	if errors.Is(err, session.ErrNoSession) {
		// Nothing to view yet. Show a placeholder and watch the sessions
		// root so the user sees the first push from any session.
		root, rerr := session.Root()
		if rerr != nil {
			return rerr
		}
		dir = root
		id = "(waiting)"
	}
	// fsnotify needs the watched directory to exist. Create the session
	// dir now so the first push doesn't race the watcher setup.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	// Best-effort: register the window-resized hook so this pane (and any
	// future ones in this tmux server) repositions and resizes itself
	// whenever tmux resizes its window. A missing hook just means no
	// auto-reflow; the TUI still works.
	_ = tmuxpane.EnsureResizeHook()

	events := make(chan tea.Msg, eventBuf)
	watcher, err := startWatcher(dir, events)
	if err != nil {
		return err
	}
	defer watcher.Close()

	m := model{sessionID: id, sessionDir: dir, events: events}
	m.reload()
	m.hmrOn = thisPaneHMROptedIn()

	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err = p.Run()
	return err
}

// startWatcher attaches an fsnotify watcher to dir and launches a single
// goroutine that pumps relevant events into ch. Returns the watcher so
// the caller can close it on shutdown.
func startWatcher(dir string, ch chan<- tea.Msg) (*fsnotify.Watcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if err := w.Add(dir); err != nil {
		w.Close()
		return nil, err
	}
	go func() {
		for {
			select {
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				if filepath.Base(ev.Name) == "stack.json" &&
					ev.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Rename) != 0 {
					ch <- reloadMsg{}
				}
			case err, ok := <-w.Errors:
				if !ok {
					return
				}
				if err != nil {
					ch <- watchErrMsg{err}
				}
			}
		}
	}()
	return w, nil
}

type model struct {
	sessionID      string
	sessionDir     string
	stacks         []store.Named
	updatedAt      time.Time
	width          int
	height         int
	err            error
	events         chan tea.Msg
	scrollAnchor   time.Time  // start of the current scroll cycle
	lastScrollMode scrollMode // last observed mode; mode changes reset the anchor
	hmrOn          bool       // refreshed from ~/.stack/hmr.pid on each tick
	refView        bool       // true while the `c` keybind is showing the top item's ref
}

type reloadMsg struct{}
type watchErrMsg struct{ err error }
type tickMsg time.Time

func (m *model) reload() {
	s, err := store.Load(m.sessionDir)
	if err != nil {
		m.err = err
		return
	}
	m.err = nil
	m.stacks = s.Stacks
	m.updatedAt = s.UpdatedAt
	// Any state change restarts the scroll cycle so the user sees the
	// new content from offset 0 rather than mid-scroll.
	m.scrollAnchor = time.Now()
}

// mainTopics is the main stack's items — the list that gets the ▶
// focus marker, the horizontal scroll animation, and the ref view.
func (m model) mainTopics() []store.Item {
	for _, n := range m.stacks {
		if n.Name == store.Main {
			return n.Topics
		}
	}
	return nil
}

// extraStacks is every non-main stack, in creation order. Rendered as
// labeled sections below main's topics.
func (m model) extraStacks() []store.Named {
	var out []store.Named
	for _, n := range m.stacks {
		if n.Name != store.Main {
			out = append(out, n)
		}
	}
	return out
}

// totalContentRows is the row count of the full content area: main's
// topics (or its 1-row empty placeholder), plus, per non-main stack, a
// blank separator and a header row, plus that stack's items. Drives
// both the vertical-scroll budget and the auto-resize height.
func (m model) totalContentRows() int {
	rows := len(m.mainTopics())
	if rows == 0 {
		rows = 1 // "(empty …)" placeholder row
	}
	for _, n := range m.extraStacks() {
		rows += 2 + len(n.Topics) // blank + "name (depth)" header + items
	}
	return rows
}

func (m model) Init() tea.Cmd {
	return tea.Batch(waitForEvent(m.events), tickCmd())
}

func tickCmd() tea.Cmd {
	return tea.Tick(tickInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// waitForEvent drains exactly one message from the watcher channel.
// Re-issued after each Update so we keep a single in-flight reader; the
// watcher itself is one goroutine for the lifetime of the program, so
// nothing accumulates.
func waitForEvent(ch <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg { return <-ch }
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			// Esc backs out of ref view first; only quits when
			// ref view is already off.
			if m.refView {
				m.refView = false
				return m, waitForEvent(m.events)
			}
			return m, tea.Quit
		case "q", "ctrl+c":
			return m, tea.Quit
		case "c":
			// Toggle the per-pane ref view (shows the top item's ref
			// if it has one). No-op when there's no top item.
			if len(m.mainTopics()) > 0 {
				m.refView = !m.refView
			}
			return m, waitForEvent(m.events)
		case "r":
			// Inside tmux, `r` does a hard reload — respawns this
			// pane with a fresh `stack tui` process so the pane
			// picks up a new binary, fresh model state, and a clean
			// view. Outside tmux there's no pane to respawn, so
			// fall back to the in-process state reload.
			if paneID := os.Getenv("TMUX_PANE"); paneID != "" {
				bin, _ := os.Executable()
				cmdLine := fmt.Sprintf("CLAUDE_CODE_SESSION_ID=%s exec %q tui", m.sessionID, bin)
				go exec.Command("tmux", "respawn-pane", "-k", "-t", paneID, cmdLine).Run()
				return m, nil
			}
			m.reload()
			m.maybeResize()
			return m, waitForEvent(m.events)
		case "h":
			// Toggle THIS pane's HMR opt-in (the @stack-hmr tmux
			// option). Auto-manages the daemon lifecycle: starts it
			// when this is the first opt-in, stops it when this was
			// the last opt-out.
			go togglePaneHMR(m.sessionID)
			return m, waitForEvent(m.events)
		}
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.maybeResize()
	case reloadMsg:
		m.reload()
		m.maybeResize()
		return m, waitForEvent(m.events)
	case watchErrMsg:
		m.err = msg.err
		return m, waitForEvent(m.events)
	case tickMsg:
		m.advanceScroll()
		m.hmrOn = thisPaneHMROptedIn()
		return m, tickCmd()
	}
	return m, nil
}

// togglePaneHMR is the body of the 'h' keypress: flips this pane's
// @stack-hmr opt-in and adjusts the daemon to match.
//
//   - opting in (0→1): start the daemon if not already running, then
//     respawn this pane so it picks up the current on-disk binary
//     immediately instead of waiting for the next .go save.
//   - opting out (1→0): stop the daemon if no other pane is opted in.
//
// Lifecycle is "start on first opt-in, stop on last opt-out" so the
// user never has to think about the daemon separately.
func togglePaneHMR(sessionID string) {
	paneID := os.Getenv("TMUX_PANE")
	if paneID == "" {
		return
	}
	on := !tmuxpane.IsHMROptedIn(paneID)
	if err := tmuxpane.SetHMROptIn(paneID, on); err != nil {
		return
	}
	bin, err := os.Executable()
	if err != nil {
		return
	}
	if on {
		// Just opted in. Make sure the daemon is up.
		if !hmrRunningSilent() {
			_ = exec.Command(bin, "hmr", "on").Run()
		}
		cmdLine := fmt.Sprintf("CLAUDE_CODE_SESSION_ID=%s exec %q tui", sessionID, bin)
		_ = exec.Command("tmux", "respawn-pane", "-k", "-t", paneID, cmdLine).Run()
		return
	}
	// Just opted out. Stop the daemon if no one else is using it.
	if !tmuxpane.AnyHMROptedIn() && hmrRunningSilent() {
		_ = exec.Command(bin, "hmr", "off").Run()
	}
}

// hmrRunningSilent is like cmd_hmr.go's hmrRunning() but never cleans
// up a stale PID file — pure read-only probe used by togglePaneHMR.
func hmrRunningSilent() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	data, err := os.ReadFile(filepath.Join(home, ".stack", "hmr.pid"))
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// thisPaneHMROptedIn is a footer/tick probe for whether HMR is "on"
// for the current pane (separate from whether the daemon is alive).
func thisPaneHMROptedIn() bool {
	paneID := os.Getenv("TMUX_PANE")
	if paneID == "" {
		return false
	}
	return tmuxpane.IsHMROptedIn(paneID)
}

// advanceScroll reacts to the per-frame tick. When the active scroll
// mode changes (e.g., the stack grew past the visible-rows budget),
// the anchor resets so the new mode starts cleanly at phase zero.
// Otherwise, the anchor is rolled forward to the next cycle once the
// current mode's hold-end elapses.
func (m *model) advanceScroll() {
	mode := m.currentScrollMode()
	if mode != m.lastScrollMode {
		m.scrollAnchor = time.Now()
		m.lastScrollMode = mode
		return
	}
	switch mode {
	case scrollHorizontal:
		cycleEnd := m.horizontalCycleEnd()
		if cycleEnd == 0 {
			return
		}
		if time.Since(m.scrollAnchor) > cycleEnd {
			m.scrollAnchor = time.Now()
		}
	case scrollVertical:
		maxOff := m.maxVerticalOffset()
		if maxOff <= 0 {
			return
		}
		scrollDur := time.Duration(maxOff) * scrollVStep
		cycleEnd := scrollStable + scrollDur + scrollVHold
		if time.Since(m.scrollAnchor) > cycleEnd {
			m.scrollAnchor = time.Now()
		}
	}
}

// currentScrollMode picks the active mode for the current frame:
// vertical takes priority when the topic count exceeds the visible
// content rows; otherwise horizontal activates when any topic exceeds
// the render width; otherwise none.
func (m model) currentScrollMode() scrollMode {
	if m.maxVerticalOffset() > 0 {
		return scrollVertical
	}
	if m.maxScrollOffset() > 0 {
		return scrollHorizontal
	}
	return scrollNone
}

// contentRows is the number of topic lines the pane can display, after
// subtracting chrome and the tail gap.
func (m model) contentRows() int {
	r := m.height - chromeAndGapRows
	if r < 0 {
		return 0
	}
	return r
}

// maxVerticalOffset is the number of content rows that won't fit on
// screen when the content area is rendered top-down from the head.
// Zero means everything fits.
func (m model) maxVerticalOffset() int {
	off := m.totalContentRows() - m.contentRows()
	if off < 0 {
		return 0
	}
	return off
}

// verticalOffset returns the current vertical-scroll position in rows.
// Stable for the first scrollStable; advances one row every scrollVStep;
// clamps at maxVerticalOffset() during the end-of-list hold.
func (m model) verticalOffset() int {
	maxOff := m.maxVerticalOffset()
	if maxOff <= 0 {
		return 0
	}
	elapsed := time.Since(m.scrollAnchor)
	if elapsed < scrollStable {
		return 0
	}
	step := int((elapsed - scrollStable) / scrollVStep)
	if step > maxOff {
		return maxOff
	}
	return step
}

// maxScrollOffset returns the largest offset needed across main's
// topics to reach their respective ends. 0 means nothing needs
// scrolling at the current pane width. Non-main sections don't animate
// — their over-width rows are simply clipped.
func (m *model) maxScrollOffset() int {
	w := m.topicRenderWidth()
	if w <= 0 {
		return 0
	}
	max := 0
	for _, t := range m.mainTopics() {
		l := len([]rune(t.Topic))
		if l <= w {
			continue
		}
		if d := l + scrollEndPad - w; d > max {
			max = d
		}
	}
	return max
}

// topicRenderWidth is the column budget for the topic text itself,
// after the 2-column "▶ " (or "  ") prefix AND a 2-column reservation
// for the optional " ·" ctxMark suffix. Reserving the ctxMark slot
// unconditionally (even when the topic has no Ref attached) costs a
// few columns of display but means a topic line can never exceed pane
// width — which would otherwise wrap into the row below and leave
// residue Bubble Tea's diff treats as "unchanged blank".
func (m model) topicRenderWidth() int {
	if m.width <= 4 {
		return 0
	}
	return m.width - 4
}

// horizontalOffset returns the rune offset for the topic at topicIdx
// under the *serial* horizontal-scroll cycle: at any moment exactly
// one over-width topic is being scrolled (top-first order); the rest
// stay at offset 0. After 10s stable, each over-width topic scrolls
// to its end, holds for scrollHoldEnd, then the next one starts.
func (m model) horizontalOffset(topicIdx, topicRunes int) int {
	w := m.topicRenderWidth()
	if topicRunes <= w {
		return 0
	}
	elapsed := time.Since(m.scrollAnchor)
	if elapsed < scrollStable {
		return 0
	}
	phaseElapsed := elapsed - scrollStable
	topics := m.mainTopics()
	for _, idx := range m.overlongTopicIndices() {
		runes := len([]rune(topics[idx].Topic)) + scrollEndPad
		topicMaxOff := runes - w
		if topicMaxOff <= 0 {
			continue
		}
		scrollDur := time.Duration(topicMaxOff) * time.Second / scrollCharsPerSec
		topicPhase := scrollDur + scrollHoldEnd
		if phaseElapsed < topicPhase {
			if idx != topicIdx {
				return 0 // not the active topic this frame
			}
			scrolled := int(phaseElapsed.Seconds() * scrollCharsPerSec)
			if scrolled > topicMaxOff {
				return topicMaxOff
			}
			return scrolled
		}
		phaseElapsed -= topicPhase
	}
	// Past the last topic's hold-end; the next advanceScroll tick will
	// reset the cycle. Until then, render at 0.
	return 0
}

// overlongTopicIndices returns the indices of main topics that exceed
// the pane render width, ordered top-first (largest index → smallest).
// Used by the serial horizontal-scroll cycle.
func (m model) overlongTopicIndices() []int {
	w := m.topicRenderWidth()
	if w <= 0 {
		return nil
	}
	topics := m.mainTopics()
	var out []int
	for i := len(topics) - 1; i >= 0; i-- {
		if len([]rune(topics[i].Topic)) > w {
			out = append(out, i)
		}
	}
	return out
}

// horizontalCycleEnd computes the total duration of one horizontal
// scroll cycle: the initial stable hold plus, per over-width topic,
// the scroll duration and the end-of-topic hold. Returns 0 if no
// topics need scrolling.
func (m model) horizontalCycleEnd() time.Duration {
	w := m.topicRenderWidth()
	if w <= 0 {
		return 0
	}
	total := time.Duration(0)
	any := false
	for _, t := range m.mainTopics() {
		runes := len([]rune(t.Topic))
		if runes <= w {
			continue
		}
		any = true
		maxOff := runes + scrollEndPad - w
		scrollDur := time.Duration(maxOff) * time.Second / scrollCharsPerSec
		total += scrollDur + scrollHoldEnd
	}
	if !any {
		return 0
	}
	return scrollStable + total
}

// maybeResize asks tmux to grow or shrink this pane to fit the current
// stack depth, keeping a constant 2-line gap between the footer and the
// pane's bottom edge. Only fires for top-placed panes (wider than tall);
// right-placed panes already span the full window height.
func (m *model) maybeResize() {
	paneID := os.Getenv("TMUX_PANE")
	if paneID == "" {
		return
	}
	// Heuristic: top-placed panes are short and wide; right-placed panes
	// are tall and narrow. We only auto-resize the short-and-wide case.
	if m.width <= m.height {
		return
	}
	winRows := queryWindowHeight()
	if winRows <= 0 {
		winRows = m.height * 2 // fallback: assume we're using half the window
	}
	desired := desiredPaneHeight(m.totalContentRows(), winRows)
	if desired == m.height {
		return
	}
	_ = exec.Command("tmux", "resize-pane", "-t", paneID, "-y", strconv.Itoa(desired)).Run()
}

// desiredPaneHeight returns the pane row count that fits contentRows
// (the full content area: main's topics or its empty placeholder, plus
// any non-main sections) plus chrome — title, blank, blank, footer —
// with a one-row tail gap below the footer so the pane never feels
// crammed against the divider. Clamped to never go below 6 rows or
// above 35 % of the window.
func desiredPaneHeight(contentRows, windowRows int) int {
	const (
		// View() renders 4 chrome rows around the content area (title,
		// blank, blank, footer) plus a 2-row tail gap. Both contribute
		// to the chrome-and-gap budget that contentRows() subtracts.
		// Must stay in sync with chromeAndGapRows.
		chromeRows = chromeAndGapRows
		// minPaneRows is one above chromeRows so contentRows() is
		// always ≥ 1 — otherwise the content loop has zero rows to
		// render into and the pane shows blank between the chrome.
		minPaneRows = chromeAndGapRows + 1
		// maxWindowPercent caps the pane at this fraction of the
		// window's row count. 35 leaves the main pane the majority of
		// the screen even when the content is deep enough to trigger
		// vertical-scroll mode.
		maxWindowPercent = 35
	)
	h := contentRows + chromeRows
	if h < minPaneRows {
		h = minPaneRows
	}
	if windowRows > 0 {
		if cap := windowRows * maxWindowPercent / 100; h > cap {
			h = cap
		}
	}
	return h
}

// queryWindowHeight returns the current tmux window's row count, or 0
// if we can't determine it (outside tmux, or tmux call failed).
func queryWindowHeight() int {
	out, err := exec.Command("tmux", "display-message", "-p", "#{window_height}").Output()
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0
	}
	return n
}

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("213"))
	subtleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	idHeadStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("255"))
	topStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("82"))
	itemStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
)

// sliceRunes returns up to maxRunes runes starting at offset off from
// the rune slice. Out-of-range slices return empty strings rather than
// panicking. width<=0 yields empty.
func sliceRunes(runes []rune, off, maxRunes int) string {
	if maxRunes <= 0 || off >= len(runes) {
		return ""
	}
	end := off + maxRunes
	if end > len(runes) {
		end = len(runes)
	}
	return string(runes[off:end])
}

// renderTopicRunes wraps sliceRunes for the topic-rendering surface:
// when off > 0 we prepend a "…" so the user can tell the visible
// window is mid-string. At the very end of scroll (the trailing window
// shows the actual last runes) the ellipsis is dropped — those final
// characters deserve a clean view, and "…" would just hide one.
func renderTopicRunes(runes []rune, off, width int) string {
	if width <= 0 || len(runes) == 0 {
		return ""
	}
	if len(runes) <= width || off == 0 {
		return sliceRunes(runes, 0, width)
	}
	if width == 1 {
		return "…"
	}
	// End-of-scroll: show the last `width` runes verbatim.
	if off+(width-1) >= len(runes) {
		return sliceRunes(runes, len(runes)-width, width)
	}
	return "…" + sliceRunes(runes, off, width-1)
}

// clipToWidth returns line truncated to at most maxCols visible columns,
// preserving ANSI escape sequences (which add no visual width) so any
// active style on the kept prefix is honored. Used as a defense-in-depth
// guard in View(): even if some construction site builds a row longer
// than the pane, the terminal still gets exactly pane-width content per
// row — no wraps, no Bubble-Tea-diff blind spots.
//
// Width is counted by rune; non-ASCII wide chars (CJK) may render off
// by one but topics in this codebase are expected to be ASCII-ish.
func clipToWidth(line string, maxCols int) string {
	if maxCols <= 0 {
		return ""
	}
	const (
		stText = iota
		stEsc  // just saw ESC, next byte is the introducer (e.g. '[')
		stCSI  // inside a CSI sequence, scanning for the final byte
	)
	var b strings.Builder
	visible := 0
	truncated := false
	state := stText
loop:
	for _, r := range line {
		switch state {
		case stText:
			if r == 0x1b {
				state = stEsc
				b.WriteRune(r)
				continue
			}
			if visible >= maxCols {
				truncated = true
				break loop
			}
			b.WriteRune(r)
			visible++
		case stEsc:
			b.WriteRune(r)
			if r == '[' {
				state = stCSI
			} else {
				// Non-CSI ESC sequences (2-byte or otherwise) end here.
				state = stText
			}
		case stCSI:
			b.WriteRune(r)
			// CSI parameter / intermediate bytes are 0x20–0x3F; the
			// final byte sits in 0x40–0x7E.
			if r >= '@' && r <= '~' {
				state = stText
			}
		}
	}
	if truncated {
		// Reset any active style so terminal state doesn't bleed past
		// the clipped row.
		b.WriteString("\x1b[0m")
	}
	return b.String()
}

// renderID shows a session id as 3 bright chars + 5 dim chars when the
// id is long enough (e.g. a UUID); shorter ids (user-supplied like
// "demo", or the "(waiting)" placeholder) render as-is.
func renderID(id string) string {
	if len(id) < 8 {
		return subtleStyle.Render(id)
	}
	return idHeadStyle.Render(id[:3]) + subtleStyle.Render(id[3:8])
}

// contentLines builds the styled rows of the content area: main's
// topics (▶ on its top — the session's current focus — with the
// horizontal scroll animation applied) or main's empty placeholder,
// then one labeled section per non-main stack in creation order.
// Non-main tops get no ▶: their top is just the most recent item, not
// the current work. Vertical windowing slices this list; the final
// clipToWidth pass guards row width.
func (m model) contentLines(mode scrollMode) []string {
	renderW := m.topicRenderWidth()
	var lines []string
	topics := m.mainTopics()
	extras := m.extraStacks()
	if len(topics) == 0 {
		if len(extras) == 0 {
			lines = append(lines, subtleStyle.Render("(empty — push a topic to get started)"))
		} else {
			lines = append(lines, subtleStyle.Render("(main empty)"))
		}
	}
	for i := len(topics) - 1; i >= 0; i-- {
		runes := []rune(topics[i].Topic)
		// Over-width topics get trailing-space breathing room so the
		// scroll lands a couple of columns shy of the right edge.
		if len(runes) > renderW {
			runes = append(runes, []rune(strings.Repeat(" ", scrollEndPad))...)
		}
		off := 0
		if mode == scrollHorizontal {
			off = m.horizontalOffset(i, len(runes))
		}
		visible := renderTopicRunes(runes, off, renderW)
		ctxMark := ""
		if topics[i].Ref != "" {
			ctxMark = subtleStyle.Render(" ·")
		}
		if i == len(topics)-1 {
			lines = append(lines, topStyle.Render("▶ "+visible)+ctxMark)
		} else {
			lines = append(lines, itemStyle.Render("  "+visible)+ctxMark)
		}
	}
	for _, n := range extras {
		lines = append(lines, "")
		if len(n.Topics) == 0 {
			lines = append(lines, subtleStyle.Render(n.Name+" (empty)"))
			continue
		}
		lines = append(lines, subtleStyle.Render(fmt.Sprintf("%s (%d)", n.Name, len(n.Topics))))
		for i := len(n.Topics) - 1; i >= 0; i-- {
			ctxMark := ""
			if n.Topics[i].Ref != "" {
				ctxMark = subtleStyle.Render(" ·")
			}
			lines = append(lines, itemStyle.Render("  "+n.Topics[i].Topic)+ctxMark)
		}
	}
	return lines
}

func (m model) View() string {
	var b strings.Builder

	// Title row.
	b.WriteString(titleStyle.Render("stack"))
	b.WriteString(" ")
	b.WriteString(subtleStyle.Render(Version))
	b.WriteString("  ")
	b.WriteString(renderID(m.sessionID))
	if !m.updatedAt.IsZero() {
		b.WriteString(subtleStyle.Render(fmt.Sprintf("  · updated %s", m.updatedAt.Local().Format("15:04:05"))))
	}
	b.WriteString("\n")

	// Separator row. Doubles as the "▲ N above" indicator when the
	// vertical scroll has hidden items above the viewport — replaces
	// the blank row that would otherwise sit between title and topics.
	mode := m.currentScrollMode()
	vOff := 0
	if mode == scrollVertical {
		vOff = m.verticalOffset()
	}
	if vOff > 0 {
		b.WriteString(subtleStyle.Render(fmt.Sprintf("▲ %d above", vOff)))
	}
	b.WriteString("\n")

	if m.err != nil {
		b.WriteString(errStyle.Render(fmt.Sprintf("error: %v", m.err)))
		b.WriteString("\n\n")
	}

	if m.refView && len(m.mainTopics()) > 0 {
		// Ref view: replace the content area with the main top item's
		// ref. Long refs wrap to the pane width; truncation is
		// the caller's problem to scroll back to (the panel is meant
		// for a glance, not a reader). An empty ref renders a
		// dim placeholder so the user understands why nothing's there.
		topics := m.mainTopics()
		top := topics[len(topics)-1]
		header := topStyle.Render("▶ "+top.Topic) + subtleStyle.Render("  · ref")
		b.WriteString(header + "\n")
		body := top.Ref
		if body == "" {
			b.WriteString(subtleStyle.Render("(no ref attached — set with `stack ref top <text>`)"))
			b.WriteString("\n")
		} else {
			for _, line := range strings.Split(body, "\n") {
				b.WriteString(itemStyle.Render(line))
				b.WriteString("\n")
			}
		}
	} else {
		lines := m.contentLines(mode)
		// Vertical windowing over the combined row list. When everything
		// fits, render it all; otherwise show contentRows() rows starting
		// at the scroll offset (the "▲ N above" indicator already names
		// what's hidden).
		if m.maxVerticalOffset() > 0 {
			end := vOff + m.contentRows()
			if end > len(lines) {
				end = len(lines)
			}
			if vOff < len(lines) {
				lines = lines[vOff:end]
			} else {
				lines = nil
			}
		}
		for _, line := range lines {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(subtleStyle.Render("r reload · "))
	if m.refView {
		b.WriteString(topStyle.Render("c ref"))
	} else {
		b.WriteString(subtleStyle.Render("c ref"))
	}
	b.WriteString(subtleStyle.Render(" · h "))
	if m.hmrOn {
		b.WriteString(topStyle.Render("hmr on"))
	} else {
		b.WriteString(subtleStyle.Render("hmr off"))
	}
	b.WriteString(subtleStyle.Render(" · q quit"))

	// Final guard: clip every emitted row to pane width. The topic-line
	// construction above already reserves the ctxMark slot, but the
	// title and footer rows have variable width and a narrow pane could
	// still overflow them.
	raw := b.String()
	if m.width <= 0 {
		return raw
	}
	lines := strings.Split(raw, "\n")
	for i, line := range lines {
		lines[i] = clipToWidth(line, m.width)
	}
	return strings.Join(lines, "\n")
}
