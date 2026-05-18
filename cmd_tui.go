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

func cmdTUI(args []string) error {
	fs := flag.NewFlagSet("tui", flag.ContinueOnError)
	sess := fs.String("session", "", "session id (default: $CLAUDE_SESSION_ID or newest)")
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

	m := model{sessionID: id, sessionDir: dir}
	m.reload()

	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err = p.Run()
	return err
}

type model struct {
	sessionID  string
	sessionDir string
	topics     []string
	updatedAt  time.Time
	width      int
	height     int
	err        error
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
	return tea.Batch(watchCmd(m.sessionDir), tickCmd())
}

func tickCmd() tea.Cmd {
	// Coalesced fsnotify events occasionally drop on macOS; a 2s
	// safety tick keeps the view honest without burning CPU.
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return reloadMsg{} })
}

func watchCmd(dir string) tea.Cmd {
	return func() tea.Msg {
		w, err := fsnotify.NewWatcher()
		if err != nil {
			return watchErrMsg{err}
		}
		if err := w.Add(dir); err != nil {
			w.Close()
			return watchErrMsg{err}
		}
		// Block until the next relevant event; the Bubble Tea
		// runtime re-invokes this command after each delivery.
		for ev := range w.Events {
			if filepath.Base(ev.Name) == "stack.json" && ev.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Rename) != 0 {
				w.Close()
				return reloadMsg{}
			}
		}
		err = <-w.Errors
		w.Close()
		return watchErrMsg{err}
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		case "r":
			m.reload()
			return m, nil
		}
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case reloadMsg:
		m.reload()
		return m, tea.Batch(watchCmd(m.sessionDir), tickCmd())
	case watchErrMsg:
		m.err = msg.err
		return m, tickCmd()
	}
	return m, nil
}

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("213"))
	subtleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	topStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("82"))
	itemStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
)

func (m model) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("stack"))
	b.WriteString("  ")
	b.WriteString(subtleStyle.Render(m.sessionID))
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
