package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/fsnotify/fsnotify"

	"stack/internal/session"
	"stack/internal/store"
)

// eventBuf is the channel depth between the watcher goroutine and the
// Bubble Tea Cmd loop. 16 is plenty: each push generates at most a few
// fsnotify ops, and the consumer drains one per Update tick.
const eventBuf = 16

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
	sessionID  string
	sessionDir string
	topics     []string
	updatedAt  time.Time
	width      int
	height     int
	err        error
	events     chan tea.Msg
}

type reloadMsg struct{}
type watchErrMsg struct{ err error }

func (m *model) reload() {
	s, err := store.Load(m.sessionDir)
	if err != nil {
		m.err = err
		return
	}
	m.err = nil
	m.topics = s.Topics
	m.updatedAt = s.UpdatedAt
}

func (m model) Init() tea.Cmd {
	return waitForEvent(m.events)
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
			return m, waitForEvent(m.events)
		}
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case reloadMsg:
		m.reload()
		return m, waitForEvent(m.events)
	case watchErrMsg:
		m.err = msg.err
		return m, waitForEvent(m.events)
	}
	return m, nil
}

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("213"))
	subtleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	idHeadStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("255"))
	topStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("82"))
	itemStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
)

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

	switch len(m.topics) {
	case 0:
		b.WriteString(subtleStyle.Render("(empty — push a topic to get started)"))
	default:
		for i := len(m.topics) - 1; i >= 0; i-- {
			topic := m.topics[i]
			if i == len(m.topics)-1 {
				b.WriteString(topStyle.Render("▶ " + topic))
			} else {
				b.WriteString(itemStyle.Render("  " + topic))
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(subtleStyle.Render("r reload · q quit"))
	return b.String()
}
