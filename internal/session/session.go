// Package session resolves "which stack are we operating on?".
//
// Order of precedence:
//  1. an explicit --session flag (passed in as `flag`)
//  2. $CLAUDE_CODE_SESSION_ID — the canonical var exposed by Claude Code
//  3. $TEST_SESSION_ID — honored for manual testing
//  4. the newest directory under ~/.stack/sessions/
//
// If a writeable session can't be resolved for a write op (push/pop),
// the resolver falls back to a literal "default" id so the call still
// succeeds.
package session

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Root returns ~/.stack/sessions, creating ~/.stack if needed.
func Root() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".stack", "sessions"), nil
}

// Dir returns the absolute path for a given session id, ensuring the
// parent (~/.stack/sessions) exists. The session dir itself is created
// lazily by store.save when the first push happens.
func Dir(id string) (string, error) {
	root, err := Root()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(root, id), nil
}

// Resolve picks the session id for a read-only operation (ls, tui).
// Returns ("", ErrNoSession) when nothing can be determined.
func Resolve(flag string) (id string, dir string, err error) {
	if flag != "" {
		d, err := Dir(flag)
		return flag, d, err
	}
	// CLAUDE_CODE_SESSION_ID is the canonical var exposed by Claude Code;
	// TEST_SESSION_ID is honored as a convenience for manual testing.
	for _, k := range []string{"CLAUDE_CODE_SESSION_ID", "TEST_SESSION_ID"} {
		if env := os.Getenv(k); env != "" {
			d, err := Dir(env)
			return env, d, err
		}
	}
	id, err = newestSession()
	if err != nil {
		return "", "", err
	}
	d, err := Dir(id)
	return id, d, err
}

// ResolveOrCreate is like Resolve but, when nothing is found, returns a
// stable fallback id ("default") so write operations always succeed.
func ResolveOrCreate(flag string) (id string, dir string, err error) {
	id, dir, err = Resolve(flag)
	if err == nil {
		return id, dir, nil
	}
	if !errors.Is(err, ErrNoSession) {
		return "", "", err
	}
	id = "default"
	dir, err = Dir(id)
	return id, dir, err
}

var ErrNoSession = errors.New("no session found")

func newestSession() (string, error) {
	root, err := Root()
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrNoSession
		}
		return "", fmt.Errorf("read %s: %w", root, err)
	}
	type item struct {
		name string
		mod  int64
	}
	var dirs []item
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		dirs = append(dirs, item{e.Name(), info.ModTime().UnixNano()})
	}
	if len(dirs) == 0 {
		return "", ErrNoSession
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].mod > dirs[j].mod })
	return dirs[0].name, nil
}
