package store

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// MaxHistory caps the undoable tail of the op log: undo reaches back
// at most this many mutations. Past it, pops are archived (snapshot
// dropped, kept for pop history up to MaxArchivedPops) and every other
// op falls off.
const (
	MaxHistory      = 100
	MaxArchivedPops = 500
)

// Entry is one mutation in a session's op log (history.jsonl, one JSON
// object per line, oldest first). Item is the item the op acted on, as
// it stands after the op; From is its prior form for ops that rewrite
// an item in place (rename, ref). Before is the full state the op
// started from — undo restores it verbatim — and is nil on archived
// pops, which are history-only. AfterHash fingerprints the state the
// op produced, so undo can tell whether anything (a hand edit, an
// unjournaled write) changed the stack since.
type Entry struct {
	At        time.Time `json:"at"`
	Op        string    `json:"op"`
	Stack     string    `json:"stack"`
	To        string    `json:"to,omitempty"`
	Item      *Item     `json:"item,omitempty"`
	From      *Item     `json:"from,omitempty"`
	Before    *Stack    `json:"before,omitempty"`
	AfterHash string    `json:"after_hash,omitempty"`
}

// Op names recorded in Entry.Op.
const (
	OpPush   = "push"
	OpPop    = "pop"
	OpMv     = "mv"
	OpRm     = "rm"
	OpRename = "rename"
	OpMove   = "move"
	OpRef    = "ref"
)

var (
	ErrNothingToUndo = errors.New("nothing to undo")
	ErrDiverged      = errors.New("stack changed since the last recorded op")
)

func HistoryPath(sessionDir string) string {
	return filepath.Join(sessionDir, "history.jsonl")
}

// hash fingerprints a state's stacks, ignoring UpdatedAt and treating
// nil and empty topic lists alike.
func hash(s Stack) string {
	stacks := make([]Named, len(s.Stacks))
	for i, n := range s.Stacks {
		stacks[i] = n
		if stacks[i].Topics == nil {
			stacks[i].Topics = []Item{}
		}
	}
	b, _ := json.Marshal(stacks)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// clone deep-copies s so a mutator can keep the pre-op snapshot while
// editing the loaded state in place.
func clone(s Stack) Stack {
	out := Stack{UpdatedAt: s.UpdatedAt, Stacks: make([]Named, len(s.Stacks))}
	for i, n := range s.Stacks {
		out.Stacks[i] = Named{Name: n.Name, Topics: append([]Item{}, n.Topics...)}
	}
	return out
}

// commit saves after and journals the op. A state-preserving op (e.g.
// moving an item onto its own position) saves but leaves no entry, so
// undo never spends a step on a no-op. stack.json is written first: if
// the process dies before the append, the log lacks the entry and the
// hash guard makes undo refuse rather than clobber.
func commit(sessionDir string, before, after Stack, e Entry) error {
	if err := save(sessionDir, after); err != nil {
		return err
	}
	afterHash := hash(after)
	if hash(before) == afterHash {
		return nil
	}
	e.At = time.Now().UTC()
	e.Before = &before
	e.AfterHash = afterHash
	entries, err := History(sessionDir)
	if err == nil {
		err = writeHistory(sessionDir, trim(append(entries, e)))
	}
	if err != nil {
		// The mutation itself landed; only undo-ability is lost.
		fmt.Fprintf(os.Stderr, "stack: warning: history not recorded: %v\n", err)
	}
	return nil
}

// trim enforces the log caps: the newest MaxHistory undoable entries
// stay whole; older pops lose their snapshot and are kept (newest
// MaxArchivedPops) for pop history; older non-pop ops are dropped.
func trim(entries []Entry) []Entry {
	// Walk newest-first, keeping the first MaxHistory undoable entries
	// whole; everything older is a candidate for the archive.
	var live, archived []Entry
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.Before != nil && len(live) < MaxHistory {
			live = append(live, e)
			continue
		}
		if e.Op != OpPop || len(archived) == MaxArchivedPops {
			continue
		}
		e.Before, e.AfterHash = nil, ""
		archived = append(archived, e)
	}
	out := make([]Entry, 0, len(archived)+len(live))
	for i := len(archived) - 1; i >= 0; i-- {
		out = append(out, archived[i])
	}
	for i := len(live) - 1; i >= 0; i-- {
		out = append(out, live[i])
	}
	return out
}

// History returns the session's op log, oldest first. A missing log is
// an empty history. Lines that don't parse are skipped with a warning
// rather than failing the whole log; the next journaled op rewrites
// the file without them.
func History(sessionDir string) ([]Entry, error) {
	p := HistoryPath(sessionDir)
	b, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", p, err)
	}
	var out []Entry
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for line := 1; sc.Scan(); line++ {
		raw := bytes.TrimSpace(sc.Bytes())
		if len(raw) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(raw, &e); err != nil {
			fmt.Fprintf(os.Stderr, "stack: warning: skipping unparseable line %d of %s: %v\n", line, p, err)
			continue
		}
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", p, err)
	}
	return out, nil
}

// writeHistory replaces the log atomically (tempfile + rename, like
// save).
func writeHistory(sessionDir string, entries []Entry) error {
	var buf bytes.Buffer
	for _, e := range entries {
		b, err := json.Marshal(e)
		if err != nil {
			return err
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	tmp, err := os.CreateTemp(sessionDir, ".history-*.jsonl")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, HistoryPath(sessionDir)); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}

// Undo reverts the newest logged op: it restores that entry's Before
// snapshot and drops the entry, so repeated undos walk further back.
// Undo itself is not journaled. Unless force is set, it refuses with
// ErrDiverged when the current state isn't the one the op produced
// (a hand edit, or a mutation whose journal write failed); with force
// it restores anyway, discarding everything since the op began.
// Archived pops carry no snapshot, so undo stops at them.
// The stack is restored before the log is trimmed, so a crash between
// the two leaves an entry the guard will refuse, never a lost undo
// that silently skips a step.
func Undo(sessionDir string, force bool) (Entry, Stack, error) {
	entries, err := History(sessionDir)
	if err != nil {
		return Entry{}, Stack{}, err
	}
	if len(entries) == 0 {
		return Entry{}, Stack{}, ErrNothingToUndo
	}
	last := entries[len(entries)-1]
	if last.Before == nil {
		return Entry{}, Stack{}, ErrNothingToUndo
	}
	if !force {
		cur, err := Load(sessionDir)
		if err != nil {
			return Entry{}, Stack{}, err
		}
		if hash(cur) != last.AfterHash {
			return last, Stack{}, ErrDiverged
		}
	}
	restored := clone(*last.Before)
	restored.normalize()
	if err := save(sessionDir, restored); err != nil {
		return Entry{}, Stack{}, err
	}
	if err := writeHistory(sessionDir, entries[:len(entries)-1]); err != nil {
		return Entry{}, Stack{}, err
	}
	return last, restored, nil
}
