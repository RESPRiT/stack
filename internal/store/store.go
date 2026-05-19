// Package store reads and writes a session's stack.json.
//
// The on-disk shape is intentionally trivial — a JSON document with a
// list of topics where the last element is the top of the stack — so a
// human (or a coding agent) can inspect and edit it directly.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Stack struct {
	Topics    []string  `json:"topics"`
	UpdatedAt time.Time `json:"updated_at"`
}

var ErrEmpty = errors.New("stack is empty")

func Path(sessionDir string) string {
	return filepath.Join(sessionDir, "stack.json")
}

func Load(sessionDir string) (Stack, error) {
	p := Path(sessionDir)
	b, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Stack{Topics: []string{}}, nil
		}
		return Stack{}, fmt.Errorf("read %s: %w", p, err)
	}
	var s Stack
	if err := json.Unmarshal(b, &s); err != nil {
		return Stack{}, fmt.Errorf("parse %s: %w", p, err)
	}
	if s.Topics == nil {
		s.Topics = []string{}
	}
	return s, nil
}

// save writes the stack atomically: write to a tempfile in the same dir,
// fsync, then rename over the target. The rename is atomic on POSIX and
// guarantees a TUI fsnotify watcher sees exactly one Write/Create event
// per save instead of a half-written file.
func save(sessionDir string, s Stack) error {
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", sessionDir, err)
	}
	s.UpdatedAt = time.Now().UTC()
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')

	target := Path(sessionDir)
	tmp, err := os.CreateTemp(sessionDir, ".stack-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, target); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}

func Push(sessionDir, topic string) (Stack, error) {
	s, err := Load(sessionDir)
	if err != nil {
		return Stack{}, err
	}
	s.Topics = append(s.Topics, topic)
	if err := save(sessionDir, s); err != nil {
		return Stack{}, err
	}
	return s, nil
}

func Pop(sessionDir string) (popped string, after Stack, err error) {
	s, err := Load(sessionDir)
	if err != nil {
		return "", Stack{}, err
	}
	if len(s.Topics) == 0 {
		return "", s, ErrEmpty
	}
	popped = s.Topics[len(s.Topics)-1]
	s.Topics = s.Topics[:len(s.Topics)-1]
	if err := save(sessionDir, s); err != nil {
		return "", Stack{}, err
	}
	return popped, s, nil
}

// posToArrayIndex maps a user-facing 1-based top-first position to the
// on-disk 0-based bottom-first array index. Pos 1 (top) -> n-1; pos n
// (bottom) -> 0. Out-of-range positions return an error.
func posToArrayIndex(pos, n int) (int, error) {
	if pos < 1 || pos > n {
		return 0, fmt.Errorf("position %d out of range (stack has %d item(s))", pos, n)
	}
	return n - pos, nil
}

// Rename replaces the topic at user-facing position pos with newText.
// pos is 1-based top-first.
func Rename(sessionDir string, pos int, newText string) (Stack, error) {
	s, err := Load(sessionDir)
	if err != nil {
		return Stack{}, err
	}
	if len(s.Topics) == 0 {
		return s, ErrEmpty
	}
	idx, err := posToArrayIndex(pos, len(s.Topics))
	if err != nil {
		return s, err
	}
	s.Topics[idx] = newText
	if err := save(sessionDir, s); err != nil {
		return Stack{}, err
	}
	return s, nil
}

// Move re-orders the topic at position from to position to. Both are
// 1-based top-first. A no-op (from == to) succeeds silently.
func Move(sessionDir string, from, to int) (Stack, error) {
	s, err := Load(sessionDir)
	if err != nil {
		return Stack{}, err
	}
	n := len(s.Topics)
	if n == 0 {
		return s, ErrEmpty
	}
	fi, err := posToArrayIndex(from, n)
	if err != nil {
		return s, err
	}
	ti, err := posToArrayIndex(to, n)
	if err != nil {
		return s, err
	}
	if fi == ti {
		return s, nil
	}
	item := s.Topics[fi]
	without := append([]string(nil), s.Topics[:fi]...)
	without = append(without, s.Topics[fi+1:]...)
	// ti was computed against the original length; it remains a valid
	// insertion index in the shorter slice because the insertion semantics
	// give the moved item exactly that array index in the resulting slice.
	if ti > len(without) {
		ti = len(without)
	}
	s.Topics = append(without[:ti], append([]string{item}, without[ti:]...)...)
	if err := save(sessionDir, s); err != nil {
		return Stack{}, err
	}
	return s, nil
}
