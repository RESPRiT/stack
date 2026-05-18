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
