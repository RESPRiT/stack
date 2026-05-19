package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"

	"stack/internal/tmuxpane"
)

// HMR (hot module reload) watches the repo's .go sources and, on every
// debounced change, rebuilds the `stack` binary and respawns the live
// TUI pane in tmux. The serving process runs in the background; the
// foreground `stack hmr` invocation only handles lifecycle.

const hmrDebounce = 250 * time.Millisecond

func cmdHMR(args []string) error {
	fs := flag.NewFlagSet("hmr", flag.ContinueOnError)
	serve := fs.Bool("serve", false, "internal: run as the background watcher daemon")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *serve {
		return hmrServe()
	}
	rest := fs.Args()
	sub := ""
	if len(rest) > 0 {
		sub = rest[0]
	}
	switch sub {
	case "on":
		return hmrOn()
	case "off":
		return hmrOff()
	case "status":
		return hmrStatus()
	case "":
		if hmrRunning() {
			return hmrOff()
		}
		return hmrOn()
	default:
		return fmt.Errorf("unknown hmr subcommand %q (use on, off, status, or no arg to toggle)", sub)
	}
}

func hmrStateDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".stack"), nil
}

func hmrPIDPath() (string, error) {
	dir, err := hmrStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "hmr.pid"), nil
}

func hmrLogPath() (string, error) {
	dir, err := hmrStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "hmr.log"), nil
}

// hmrRunning is true iff the PID file points at a live process.
// A stale PID file (process died unclean) is treated as not running
// and removed on read so subsequent calls don't have to re-check.
func hmrRunning() bool {
	pidPath, err := hmrPIDPath()
	if err != nil {
		return false
	}
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		_ = os.Remove(pidPath)
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		_ = os.Remove(pidPath)
		return false
	}
	// Signal 0 is a liveness probe; no signal is delivered.
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		_ = os.Remove(pidPath)
		return false
	}
	return true
}

func hmrOn() error {
	if hmrRunning() {
		fmt.Println("HMR already running")
		return nil
	}
	// Confirm we have a place to build from.
	repo, err := findRepoRoot()
	if err != nil {
		return err
	}
	// And a pane to respawn.
	paneID, _, err := tmuxpane.TUIPaneID()
	if err != nil {
		return err
	}
	if paneID == "" {
		return errors.New("no @stack-tui pane to drive — run `stack open` first")
	}

	bin, err := os.Executable()
	if err != nil {
		return err
	}
	stateDir, err := hmrStateDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}
	logPath, _ := hmrLogPath()
	logF, err := os.Create(logPath)
	if err != nil {
		return err
	}
	defer logF.Close()

	cmd := exec.Command(bin, "hmr", "--serve")
	cmd.Dir = repo
	cmd.Stdout = logF
	cmd.Stderr = logF
	// Detach from the controlling tty so closing the invoking shell
	// doesn't kill the daemon.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	pid := cmd.Process.Pid
	pidPath, _ := hmrPIDPath()
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(pid)+"\n"), 0o644); err != nil {
		_ = cmd.Process.Kill()
		return err
	}
	_ = cmd.Process.Release()
	fmt.Printf("HMR started (pid %d, watching %s, log %s)\n", pid, repo, logPath)
	return nil
}

func hmrOff() error {
	pidPath, _ := hmrPIDPath()
	data, err := os.ReadFile(pidPath)
	if err != nil {
		fmt.Println("HMR not running")
		return nil
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	if pid > 0 {
		if proc, err := os.FindProcess(pid); err == nil {
			_ = proc.Signal(syscall.SIGTERM)
		}
	}
	_ = os.Remove(pidPath)
	fmt.Println("HMR stopped")
	return nil
}

func hmrStatus() error {
	if !hmrRunning() {
		fmt.Println("HMR not running")
		return nil
	}
	pidPath, _ := hmrPIDPath()
	data, _ := os.ReadFile(pidPath)
	pid := strings.TrimSpace(string(data))
	logPath, _ := hmrLogPath()
	fmt.Printf("HMR running (pid %s, log %s)\n", pid, logPath)
	return nil
}

// findRepoRoot walks up from the current working directory looking for
// a go.mod. Returns the repo's absolute path or an error if not found.
func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no go.mod found in cwd or any parent — run `stack hmr` from the stack repo")
		}
		dir = parent
	}
}

// hmrServe runs the actual watcher loop. It is invoked as a detached
// background process by hmrOn(); foreground use is unsupported.
func hmrServe() error {
	repo, err := findRepoRoot()
	if err != nil {
		return err
	}
	paneID, sessionID, err := tmuxpane.TUIPaneID()
	if err != nil {
		return err
	}
	if paneID == "" {
		return errors.New("no @stack-tui pane to drive")
	}

	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer w.Close()

	if err := addRepoDirs(w, repo); err != nil {
		return err
	}

	// Tear down cleanly on SIGTERM (sent by `stack hmr off`).
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	fmt.Printf("[hmr] watching %s, target pane %s\n", repo, paneID)

	// Initial build+respawn so the user sees a clean baseline.
	doRebuild(repo, paneID, sessionID)

	var (
		debounceTimer  *time.Timer
		debounceTimerC <-chan time.Time
	)
	for {
		select {
		case ev, ok := <-w.Events:
			if !ok {
				return nil
			}
			if !hmrInterestingEvent(ev) {
				continue
			}
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			debounceTimer = time.NewTimer(hmrDebounce)
			debounceTimerC = debounceTimer.C
		case <-debounceTimerC:
			doRebuild(repo, paneID, sessionID)
			debounceTimer = nil
			debounceTimerC = nil
		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			fmt.Printf("[hmr] watch error: %v\n", err)
		case <-sigCh:
			fmt.Println("[hmr] shutting down")
			return nil
		}
	}
}

// addRepoDirs walks repo and adds every directory to the watcher except
// for paths inside .git, bin, or any other excluded dirs.
func addRepoDirs(w *fsnotify.Watcher, repo string) error {
	return filepath.WalkDir(repo, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		name := d.Name()
		if name == ".git" || name == "bin" || name == "node_modules" {
			return filepath.SkipDir
		}
		return w.Add(p)
	})
}

// hmrInterestingEvent filters fsnotify events to only those that should
// trigger a rebuild: Go source, go.mod, go.sum, and SKILL.md changes.
// Excludes anything under bin/, .git/, or with editor swap suffixes.
func hmrInterestingEvent(ev fsnotify.Event) bool {
	if ev.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Rename) == 0 {
		return false
	}
	name := ev.Name
	if strings.HasSuffix(name, "~") || strings.HasSuffix(name, ".swp") {
		return false
	}
	if strings.Contains(name, "/.git/") || strings.Contains(name, "/bin/") {
		return false
	}
	base := filepath.Base(name)
	switch base {
	case "go.mod", "go.sum", "SKILL.md":
		return true
	}
	return strings.HasSuffix(base, ".go")
}

// doRebuild runs `go install .` in the repo and, on success, respawns
// the TUI pane so the new binary is picked up immediately.
func doRebuild(repo, paneID, sessionID string) {
	t0 := time.Now()
	build := exec.Command("go", "install", ".")
	build.Dir = repo
	out, err := build.CombinedOutput()
	if err != nil {
		fmt.Printf("[hmr] build failed (%s):\n%s\n", time.Since(t0).Round(time.Millisecond), out)
		return
	}
	bin, _ := os.Executable()
	cmdLine := fmt.Sprintf("CLAUDE_CODE_SESSION_ID=%s exec %q tui", sessionID, bin)
	respawn := exec.Command("tmux", "respawn-pane", "-k", "-t", paneID, cmdLine)
	if err := respawn.Run(); err != nil {
		fmt.Printf("[hmr] respawn failed: %v\n", err)
		return
	}
	fmt.Printf("[hmr] rebuilt + respawned %s (%s)\n", paneID, time.Since(t0).Round(time.Millisecond))
}
