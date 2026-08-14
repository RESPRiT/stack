// Package store reads and writes a session's stack.json.
//
// The on-disk shape is intentionally trivial — a JSON document holding
// named stacks, each a list of topics where the last element is the top
// — so a human (or a coding agent) can inspect and edit it directly.
//
//	{
//	  "stacks": [
//	    {"name": "main", "topics": ["older", "top of main"]},
//	    {"name": "backlog", "topics": ["an idea"]}
//	  ],
//	  "updated_at": "..."
//	}
//
// The "main" stack always exists, is always first, and is the session's
// current focus. Other stacks keep their creation order. Legacy files
// with a top-level `topics` array load as main's topics and adopt the
// new shape on the next save.
//
// Each entry in a `topics` array is either a bare string (just the
// topic, no ref) or a `{"topic": "...", "ref": "..."}` object. Ref-less
// items stay as bare strings on save so hand-editing remains easy for
// the common case.
package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Main is the name of the default stack: the one commands target
// without --stack, and the one whose top is the session's current
// focus. It always exists and cannot be deleted.
const Main = "main"

// MaxRefBytes is the soft size cap for a single Item's Ref. Saves
// above this size still proceed; a warning is written to os.Stderr so
// the user notices the bloat.
const MaxRefBytes = 1024

// Item is one entry on a stack: the topic text plus an optional
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

// Named is one named stack: a name plus its topics, bottom-first (the
// last element is the top).
type Named struct {
	Name   string `json:"name"`
	Topics []Item `json:"topics"`
}

// Stack is a session's full stack state: the ordered set of named
// stacks. Stacks[0] is always Main.
type Stack struct {
	Stacks    []Named   `json:"stacks"`
	UpdatedAt time.Time `json:"updated_at"`
}

var (
	ErrEmpty   = errors.New("stack is empty")
	ErrNoStack = errors.New("no such stack")
)

// noStackErr wraps ErrNoStack with the requested name and the names
// that do exist, so CLI errors are self-explanatory.
func (s Stack) noStackErr(name string) error {
	return fmt.Errorf("%w: %q (have: %s)", ErrNoStack, name, strings.Join(s.Names(), ", "))
}

// find returns a pointer to the named stack, or nil.
func (s *Stack) find(name string) *Named {
	for i := range s.Stacks {
		if s.Stacks[i].Name == name {
			return &s.Stacks[i]
		}
	}
	return nil
}

// Get returns the named stack's topics and whether the stack exists.
func (s Stack) Get(name string) ([]Item, bool) {
	if n := (&s).find(name); n != nil {
		return n.Topics, true
	}
	return nil, false
}

// Depth returns the named stack's topic count (0 if it doesn't exist).
func (s Stack) Depth(name string) int {
	topics, _ := s.Get(name)
	return len(topics)
}

// Names returns the stack names in display order: Main first, then
// creation order.
func (s Stack) Names() []string {
	out := make([]string, len(s.Stacks))
	for i, n := range s.Stacks {
		out[i] = n.Name
	}
	return out
}

// normalize guarantees the Main stack exists at index 0 and that no
// stack has nil Topics. Called on every load so both migrated legacy
// files and hand-edited ones end up in canonical shape.
func (s *Stack) normalize() {
	for i := range s.Stacks {
		if s.Stacks[i].Topics == nil {
			s.Stacks[i].Topics = []Item{}
		}
	}
	for i := range s.Stacks {
		if s.Stacks[i].Name == Main {
			if i != 0 {
				m := s.Stacks[i]
				s.Stacks = append(s.Stacks[:i], s.Stacks[i+1:]...)
				s.Stacks = append([]Named{m}, s.Stacks...)
			}
			return
		}
	}
	s.Stacks = append([]Named{{Name: Main, Topics: []Item{}}}, s.Stacks...)
}

func Path(sessionDir string) string {
	return filepath.Join(sessionDir, "stack.json")
}

func Load(sessionDir string) (Stack, error) {
	p := Path(sessionDir)
	b, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s := Stack{}
			s.normalize()
			return s, nil
		}
		return Stack{}, fmt.Errorf("read %s: %w", p, err)
	}
	// Accept both shapes: the current {"stacks": [...]} document and the
	// legacy {"topics": [...]} one, which migrates to main's topics.
	var raw struct {
		Stacks    []Named   `json:"stacks"`
		Topics    []Item    `json:"topics"`
		UpdatedAt time.Time `json:"updated_at"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return Stack{}, fmt.Errorf("parse %s: %w", p, err)
	}
	s := Stack{Stacks: raw.Stacks, UpdatedAt: raw.UpdatedAt}
	if s.Stacks == nil && raw.Topics != nil {
		s.Stacks = []Named{{Name: Main, Topics: raw.Topics}}
	}
	s.normalize()
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
	for _, st := range s.Stacks {
		for _, it := range st.Topics {
			if n := len(it.Ref); n > MaxRefBytes {
				fmt.Fprintf(os.Stderr, "stack: warning: ref for %q is %d bytes (cap %d)\n",
					it.Topic, n, MaxRefBytes)
			}
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

// Push appends a topic to the named stack, creating the stack if it
// doesn't exist yet (lazy creation — there is no separate "new stack"
// verb).
func Push(sessionDir, name, topic string) (Stack, error) {
	s, err := Load(sessionDir)
	if err != nil {
		return Stack{}, err
	}
	n := s.find(name)
	if n == nil {
		s.Stacks = append(s.Stacks, Named{Name: name, Topics: []Item{}})
		n = &s.Stacks[len(s.Stacks)-1]
	}
	n.Topics = append(n.Topics, Item{Topic: topic})
	if err := save(sessionDir, s); err != nil {
		return Stack{}, err
	}
	return s, nil
}

// Pop removes and returns the named stack's top item, plus the stack's
// remaining topics. Unknown names error (only Push creates stacks); an
// emptied stack persists until Delete.
func Pop(sessionDir, name string) (popped Item, after []Item, err error) {
	s, err := Load(sessionDir)
	if err != nil {
		return Item{}, nil, err
	}
	n := s.find(name)
	if n == nil {
		return Item{}, nil, s.noStackErr(name)
	}
	if len(n.Topics) == 0 {
		return Item{}, nil, ErrEmpty
	}
	popped = n.Topics[len(n.Topics)-1]
	n.Topics = n.Topics[:len(n.Topics)-1]
	if err := save(sessionDir, s); err != nil {
		return Item{}, nil, err
	}
	return popped, n.Topics, nil
}

// Delete removes the named stack entirely. Main is undeletable; unknown
// names error. Returns the deleted stack's topic count.
func Delete(sessionDir, name string) (int, error) {
	if name == Main {
		return 0, fmt.Errorf("cannot delete the %q stack", Main)
	}
	s, err := Load(sessionDir)
	if err != nil {
		return 0, err
	}
	n := s.find(name)
	if n == nil {
		return 0, s.noStackErr(name)
	}
	depth := len(n.Topics)
	for i := range s.Stacks {
		if s.Stacks[i].Name == name {
			s.Stacks = append(s.Stacks[:i], s.Stacks[i+1:]...)
			break
		}
	}
	if err := save(sessionDir, s); err != nil {
		return 0, err
	}
	return depth, nil
}

// MoveItem removes the item at user-facing 1-based top-first position
// pos from the named source stack and pushes it onto the destination
// stack's top, ref included — the atomic cross-stack move that a manual
// pop + re-push can't do without dropping the ref. The destination is
// created if it doesn't exist (like Push); source and destination must
// differ (use Move for reordering within one stack).
func MoveItem(sessionDir, fromName string, pos int, toName string) (Item, Stack, error) {
	if fromName == toName {
		return Item{}, Stack{}, fmt.Errorf("source and destination are both %q (use edit move to reorder within a stack)", fromName)
	}
	s, err := Load(sessionDir)
	if err != nil {
		return Item{}, Stack{}, err
	}
	src := s.find(fromName)
	if src == nil {
		return Item{}, Stack{}, s.noStackErr(fromName)
	}
	if len(src.Topics) == 0 {
		return Item{}, Stack{}, ErrEmpty
	}
	idx, err := posToArrayIndex(pos, len(src.Topics))
	if err != nil {
		return Item{}, Stack{}, err
	}
	item := src.Topics[idx]
	src.Topics = append(src.Topics[:idx], src.Topics[idx+1:]...)
	dst := s.find(toName)
	if dst == nil {
		s.Stacks = append(s.Stacks, Named{Name: toName, Topics: []Item{}})
		dst = &s.Stacks[len(s.Stacks)-1]
	}
	dst.Topics = append(dst.Topics, item)
	if err := save(sessionDir, s); err != nil {
		return Item{}, Stack{}, err
	}
	return item, s, nil
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

// mutate loads the session, resolves the named stack (erroring on
// unknown names or an empty stack), applies fn to it, and saves. The
// shared shape of Rename/Move/SetRef.
func mutate(sessionDir, name string, fn func(*Named) error) (Stack, error) {
	s, err := Load(sessionDir)
	if err != nil {
		return Stack{}, err
	}
	n := s.find(name)
	if n == nil {
		return s, s.noStackErr(name)
	}
	if len(n.Topics) == 0 {
		return s, ErrEmpty
	}
	if err := fn(n); err != nil {
		return s, err
	}
	if err := save(sessionDir, s); err != nil {
		return Stack{}, err
	}
	return s, nil
}

// Rename replaces the topic at user-facing position pos with newText.
// The ref (if any) is preserved. pos is 1-based top-first.
func Rename(sessionDir, name string, pos int, newText string) (Stack, error) {
	return mutate(sessionDir, name, func(n *Named) error {
		idx, err := posToArrayIndex(pos, len(n.Topics))
		if err != nil {
			return err
		}
		n.Topics[idx].Topic = newText
		return nil
	})
}

// Move re-orders the topic at position from to position to. Both are
// 1-based top-first. A no-op (from == to) succeeds silently. Ref
// travels with the item.
func Move(sessionDir, name string, from, to int) (Stack, error) {
	return mutate(sessionDir, name, func(n *Named) error {
		fi, err := posToArrayIndex(from, len(n.Topics))
		if err != nil {
			return err
		}
		ti, err := posToArrayIndex(to, len(n.Topics))
		if err != nil {
			return err
		}
		if fi == ti {
			return nil
		}
		item := n.Topics[fi]
		without := append([]Item(nil), n.Topics[:fi]...)
		without = append(without, n.Topics[fi+1:]...)
		if ti > len(without) {
			ti = len(without)
		}
		n.Topics = append(without[:ti], append([]Item{item}, without[ti:]...)...)
		return nil
	})
}

// SetRef sets the ref for the item at user-facing 1-based top-first
// position pos. An empty string clears the ref (the item reverts to a
// bare string on disk).
func SetRef(sessionDir, name string, pos int, ref string) (Stack, error) {
	return mutate(sessionDir, name, func(n *Named) error {
		idx, err := posToArrayIndex(pos, len(n.Topics))
		if err != nil {
			return err
		}
		n.Topics[idx].Ref = ref
		return nil
	})
}
