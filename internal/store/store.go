// Package store reads and writes a session's stack.json.
//
// The on-disk shape is intentionally trivial — a JSON document with a
// list of topics where the last element is the top of the stack — so a
// human (or a coding agent) can inspect and edit it directly.
//
// Each entry in `topics` is either a bare string (just the topic, no
// ref) or a `{"topic": "...", "ref": "..."}` object. Ref-less items
// stay as bare strings on save so hand-editing remains easy for the
// common case.
package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// MaxRefBytes is the soft size cap for a single Item's Ref. Saves
// above this size still proceed; a warning is written to os.Stderr so
// the user notices the bloat.
const MaxRefBytes = 1024

// Item is one entry on the stack: the topic text plus an optional
// free-form ref the user (or agent) attaches for future reference. An
// Item with empty Ref marshals as a bare JSON string so the stack
// file stays hand-editable for the common case.
type Item struct {
	Topic string
	Ref   string
}

// MarshalJSON emits a bare string when no ref is set, or a
// {"topic", "ref"} object when one is. The asymmetric shape keeps
// short, ref-less stacks readable on disk.
func (i Item) MarshalJSON() ([]byte, error) {
	if i.Ref == "" {
		return json.Marshal(i.Topic)
	}
	return json.Marshal(struct {
		Topic string `json:"topic"`
		Ref   string `json:"ref"`
	}{i.Topic, i.Ref})
}

// UnmarshalJSON accepts either a bare string (legacy / ref-less) or
// an object with topic + ref. Anything else is a parse error.
func (i *Item) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) > 0 && data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		i.Topic = s
		i.Ref = ""
		return nil
	}
	var obj struct {
		Topic string `json:"topic"`
		Ref   string `json:"ref"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return fmt.Errorf("stack item: expected string or {topic, ref} object: %w", err)
	}
	i.Topic = obj.Topic
	i.Ref = obj.Ref
	return nil
}

type Stack struct {
	Topics    []Item    `json:"topics"`
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
			return Stack{Topics: []Item{}}, nil
		}
		return Stack{}, fmt.Errorf("read %s: %w", p, err)
	}
	var s Stack
	if err := json.Unmarshal(b, &s); err != nil {
		return Stack{}, fmt.Errorf("parse %s: %w", p, err)
	}
	if s.Topics == nil {
		s.Topics = []Item{}
	}
	return s, nil
}

// save writes the stack atomically: write to a tempfile in the same
// dir, fsync, then rename over the target. The rename is atomic on
// POSIX and guarantees a TUI fsnotify watcher sees exactly one
// Write/Create event per save instead of a half-written file.
//
// Any Item.Ref above MaxRefBytes emits a single-line warning to
// os.Stderr — the save still proceeds (advisory cap, not enforced).
func save(sessionDir string, s Stack) error {
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", sessionDir, err)
	}
	s.UpdatedAt = time.Now().UTC()
	for _, it := range s.Topics {
		if n := len(it.Ref); n > MaxRefBytes {
			fmt.Fprintf(os.Stderr, "stack: warning: ref for %q is %d bytes (cap %d)\n",
				it.Topic, n, MaxRefBytes)
		}
	}
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
	s.Topics = append(s.Topics, Item{Topic: topic})
	if err := save(sessionDir, s); err != nil {
		return Stack{}, err
	}
	return s, nil
}

func Pop(sessionDir string) (popped Item, after Stack, err error) {
	s, err := Load(sessionDir)
	if err != nil {
		return Item{}, Stack{}, err
	}
	if len(s.Topics) == 0 {
		return Item{}, s, ErrEmpty
	}
	popped = s.Topics[len(s.Topics)-1]
	s.Topics = s.Topics[:len(s.Topics)-1]
	if err := save(sessionDir, s); err != nil {
		return Item{}, Stack{}, err
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
// The ref (if any) is preserved. pos is 1-based top-first.
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
	s.Topics[idx].Topic = newText
	if err := save(sessionDir, s); err != nil {
		return Stack{}, err
	}
	return s, nil
}

// Move re-orders the topic at position from to position to. Both are
// 1-based top-first. A no-op (from == to) succeeds silently. Ref
// travels with the item.
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
	without := append([]Item(nil), s.Topics[:fi]...)
	without = append(without, s.Topics[fi+1:]...)
	if ti > len(without) {
		ti = len(without)
	}
	s.Topics = append(without[:ti], append([]Item{item}, without[ti:]...)...)
	if err := save(sessionDir, s); err != nil {
		return Stack{}, err
	}
	return s, nil
}

// SetRef sets the ref for the item at user-facing 1-based top-first
// position pos. An empty string clears the ref (the item reverts to a
// bare string on disk).
func SetRef(sessionDir string, pos int, ref string) (Stack, error) {
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
	s.Topics[idx].Ref = ref
	if err := save(sessionDir, s); err != nil {
		return Stack{}, err
	}
	return s, nil
}
