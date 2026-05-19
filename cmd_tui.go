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
	// scrollHoldEnd is the pause after horizontal scrolling reaches the
	// end before the cycle resets to offset 0.
	scrollHoldEnd = 10 * time.Second
	// scrollVStep advances the vertical view by one row every this often.
	scrollVStep = 1 * time.Second
	// scrollVHold is the pause at the bottom of the list before the
	// vertical cycle restarts.
	scrollVHold = 30 * time.Second
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

func cmdTUI(args []string) error {
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

	// Best-effort: register the client-resized hook so this pane (and any
	// future ones in this tmux server) repositions itself on resize. A
	// missing hook just means no auto-reflow; the TUI still works.
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
	sessionID    string
	sessionDir   string
	topics       []string
	updatedAt    time.Time
	width        int
	height       int
	err          error
	events         chan tea.Msg
	scrollAnchor   time.Time  // start of the current scroll cycle
	lastScrollMode scrollMode // last observed mode; mode changes reset the anchor
	hmrOn          bool       // refreshed from ~/.stack/hmr.pid on each tick
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
	m.topics = s.Topics
	m.updatedAt = s.UpdatedAt
	// Any state change restarts the scroll cycle so the user sees the
	// new content from offset 0 rather than mid-scroll.
	m.scrollAnchor = time.Now()
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
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
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
			go togglePaneHMR()
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
//   - opting in (0→1): start the daemon if not already running.
//   - opting out (1→0): stop the daemon if no other pane is opted in.
//
// Lifecycle is "start on first opt-in, stop on last opt-out" so the
// user never has to think about the daemon separately.
func togglePaneHMR() {
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

// maxVerticalOffset is the number of topics that won't fit on screen
// when the topic list is rendered top-down from the head. Zero means
// the whole stack fits.
func (m model) maxVerticalOffset() int {
	off := len(m.topics) - m.contentRows()
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

// maxScrollOffset returns the largest offset needed across all topics to
// reach their respective ends. 0 means nothing needs scrolling at the
// current pane width.
func (m *model) maxScrollOffset() int {
	w := m.topicRenderWidth()
	if w <= 0 {
		return 0
	}
	max := 0
	for _, t := range m.topics {
		l := len([]rune(t))
		if d := l - w; d > max {
			max = d
		}
	}
	return max
}

// topicRenderWidth is the column budget for the topic text itself,
// after the 2-column "▶ " (or "  ") prefix.
func (m model) topicRenderWidth() int {
	if m.width <= 2 {
		return 0
	}
	return m.width - 2
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
	for _, idx := range m.overlongTopicIndices() {
		runes := len([]rune(m.topics[idx]))
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

// overlongTopicIndices returns the indices of topics that exceed the
// pane render width, ordered top-first (largest index → smallest).
// Used by the serial horizontal-scroll cycle.
func (m model) overlongTopicIndices() []int {
	w := m.topicRenderWidth()
	if w <= 0 {
		return nil
	}
	var out []int
	for i := len(m.topics) - 1; i >= 0; i-- {
		if len([]rune(m.topics[i])) > w {
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
	for _, t := range m.topics {
		runes := len([]rune(t))
		if runes <= w {
			continue
		}
		any = true
		maxOff := runes - w
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
	desired := desiredPaneHeight(len(m.topics), winRows)
	if desired == m.height {
		return
	}
	_ = exec.Command("tmux", "resize-pane", "-t", paneID, "-y", strconv.Itoa(desired)).Run()
}

// desiredPaneHeight returns the pane row count that fits the current
// stack content plus chrome — title, blank, topics (or empty
// placeholder), blank, footer — with a one-row tail gap below the
// footer so the pane never feels crammed against the divider.
// Clamped to never go below 6 rows or above 35 % of the window.
func desiredPaneHeight(stackLen, windowRows int) int {
	const (
		// View() renders 4 chrome rows around the topics list (title,
		// blank, blank, footer) plus a 2-row tail gap. Both contribute
		// to the chrome-and-gap budget that contentRows() subtracts.
		// Must stay in sync with chromeAndGapRows.
		chromeRows   = chromeAndGapRows
		emptyContent = 1 // "(empty — push a topic …)"
		// minPaneRows is one above chromeRows so contentRows() is
		// always ≥ 1 — otherwise the topic loop has zero rows to
		// render into and the pane shows blank between the chrome.
		minPaneRows = chromeAndGapRows + 1
		// maxWindowPercent caps the pane at this fraction of the
		// window's row count. 35 leaves the main pane the majority of
		// the screen even when the stack is deep enough to trigger
		// vertical-scroll mode.
		maxWindowPercent = 35
	)
	content := stackLen
	if stackLen == 0 {
		content = emptyContent
	}
	h := content + chromeRows
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

// renderID shows a session id as 3 bright chars + 5 dim chars when the
// id is long enough (e.g. a UUID); shorter ids (user-supplied like
// "demo", or the "(waiting)" placeholder) render as-is.
func renderID(id string) string {
	if len(id) < 8 {
		return subtleStyle.Render(id)
	}
	return idHeadStyle.Render(id[:3]) + subtleStyle.Render(id[3:8])
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

	if len(m.topics) == 0 {
		b.WriteString(subtleStyle.Render("(empty — push a topic to get started)"))
		// Topic loop writes a trailing \n per item so the blank line
		// before the footer falls out naturally; the empty branch has
		// to add one explicitly to keep that blank in the layout.
		b.WriteString("\n")
	} else {
		renderW := m.topicRenderWidth()

		// Pick the index range to render. Top-of-stack first.
		topI := len(m.topics) - 1
		botI := 0
		if mode == scrollVertical {
			topI -= vOff
			botI = topI - m.contentRows() + 1
			if botI < 0 {
				botI = 0
			}
		}

		for i := topI; i >= botI; i-- {
			runes := []rune(m.topics[i])
			off := 0
			if mode == scrollHorizontal {
				off = m.horizontalOffset(i, len(runes))
			}
			visible := renderTopicRunes(runes, off, renderW)
			if i == len(m.topics)-1 {
				b.WriteString(topStyle.Render("▶ " + visible))
			} else {
				b.WriteString(itemStyle.Render("  " + visible))
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(subtleStyle.Render("r reload · h "))
	if m.hmrOn {
		b.WriteString(topStyle.Render("hmr on"))
	} else {
		b.WriteString(subtleStyle.Render("hmr off"))
	}
	b.WriteString(subtleStyle.Render(" · q quit"))
	return b.String()
}
