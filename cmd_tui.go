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
	scrollCharsPerSec = 5
	// scrollHoldEnd is the pause after horizontal scrolling reaches the
	// end before the cycle resets to offset 0.
	scrollHoldEnd = 10 * time.Second
	// scrollVStep advances the vertical view by one row every this often.
	scrollVStep = 30 * time.Second
	// scrollVHold is the pause at the bottom of the list before the
	// vertical cycle restarts.
	scrollVHold = 30 * time.Second
	// tickInterval drives the scroll animation. 5 chars/sec implies a
	// 200 ms tick keeps the motion smooth (one char per tick).
	tickInterval = 200 * time.Millisecond

	// View() chrome + tail gap. The tail gap is the user-visible 2-row
	// space between the footer and the panel divider; chrome is title +
	// blank + blank + footer.
	chromeAndGapRows = 6
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
			m.reload()
			m.maybeResize()
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
		return m, tickCmd()
	}
	return m, nil
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
		maxOff := m.maxScrollOffset()
		if maxOff <= 0 {
			return
		}
		scrollDur := time.Duration(maxOff) * time.Second / scrollCharsPerSec
		cycleEnd := scrollStable + scrollDur + scrollHoldEnd
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

// horizontalOffset returns how many runes to skip at the start of a
// topic of the given rune length, given the current scroll cycle.
// Returns 0 during the stable phase, advances at scrollCharsPerSec,
// then holds at maxOff during the end-of-scroll pause.
func (m model) horizontalOffset(topicRunes int) int {
	w := m.topicRenderWidth()
	if topicRunes <= w {
		return 0
	}
	maxOff := topicRunes - w
	elapsed := time.Since(m.scrollAnchor)
	if elapsed < scrollStable {
		return 0
	}
	scrolled := int((elapsed - scrollStable).Seconds() * scrollCharsPerSec)
	if scrolled > maxOff {
		return maxOff
	}
	return scrolled
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
// placeholder), blank, footer — with a two-row tail gap below the
// footer so the pane never feels crammed against the divider.
// Clamped to never go below 6 rows or above 35 % of the window.
func desiredPaneHeight(stackLen, windowRows int) int {
	const (
		// View() renders 4 chrome rows around the topics list:
		// title, blank, blank, footer. The +1 here is the tail gap;
		// tmux contributes the second gap row (an off-by-one in
		// resize-pane -y vs reported pane_height).
		chromeRows   = 5
		emptyContent = 1 // "(empty — push a topic …)"
		minPaneRows  = 6
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
	b.WriteString(titleStyle.Render("stack"))
	b.WriteString(" ")
	b.WriteString(subtleStyle.Render(Version))
	b.WriteString("  ")
	b.WriteString(renderID(m.sessionID))
	if !m.updatedAt.IsZero() {
		b.WriteString(subtleStyle.Render(fmt.Sprintf("  · updated %s", m.updatedAt.Local().Format("15:04:05"))))
	}
	b.WriteString("\n\n")

	if m.err != nil {
		b.WriteString(errStyle.Render(fmt.Sprintf("error: %v", m.err)))
		b.WriteString("\n\n")
	}

	if len(m.topics) == 0 {
		b.WriteString(subtleStyle.Render("(empty — push a topic to get started)"))
	} else {
		mode := m.currentScrollMode()
		renderW := m.topicRenderWidth()

		// Pick the index range to render. Top-of-stack first.
		topI := len(m.topics) - 1
		botI := 0
		if mode == scrollVertical {
			vOff := m.verticalOffset()
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
				off = m.horizontalOffset(len(runes))
			}
			visible := sliceRunes(runes, off, renderW)
			if i == len(m.topics)-1 {
				b.WriteString(topStyle.Render("▶ " + visible))
			} else {
				b.WriteString(itemStyle.Render("  " + visible))
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(subtleStyle.Render("r reload · q quit"))
	return b.String()
}
