package store

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
)

// stateOf loads dir and returns its stacks, failing the test on error.
func stateOf(t *testing.T, dir string) []Named {
	t.Helper()
	s, err := Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return s.Stacks
}

func mustUndo(t *testing.T, dir string) Entry {
	t.Helper()
	e, _, err := Undo(dir, false)
	if err != nil {
		t.Fatalf("undo: %v", err)
	}
	return e
}

func TestUndo_RoundTripsEveryOp(t *testing.T) {
	dir := t.TempDir()
	if _, err := Push(dir, Main, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := Push(dir, Main, "b"); err != nil {
		t.Fatal(err)
	}
	if _, err := Push(dir, "backlog", "idea"); err != nil {
		t.Fatal(err)
	}
	ops := []struct {
		op string
		do func() error
	}{
		{OpPush, func() error { _, err := Push(dir, Main, "c"); return err }},
		{OpPop, func() error { _, _, err := Pop(dir, Main); return err }},
		{OpRename, func() error { _, err := Rename(dir, Main, 1, "b2"); return err }},
		{OpMove, func() error { _, err := Move(dir, Main, 1, 2); return err }},
		{OpRef, func() error { _, err := SetRef(dir, Main, 1, "note"); return err }},
		{OpMv, func() error { _, _, err := MoveItem(dir, "backlog", 1, Main); return err }},
		{OpRm, func() error { _, err := Delete(dir, "backlog"); return err }},
	}
	for _, tc := range ops {
		t.Run(tc.op, func(t *testing.T) {
			before := stateOf(t, dir)
			if err := tc.do(); err != nil {
				t.Fatalf("%s: %v", tc.op, err)
			}
			if reflect.DeepEqual(stateOf(t, dir), before) {
				t.Fatalf("%s changed nothing", tc.op)
			}
			if e := mustUndo(t, dir); e.Op != tc.op {
				t.Errorf("undid %q, want %q", e.Op, tc.op)
			}
			if got := stateOf(t, dir); !reflect.DeepEqual(got, before) {
				t.Errorf("after undo: got %+v, want %+v", got, before)
			}
		})
	}
}

func TestUndo_WalksBackAndEmpties(t *testing.T) {
	dir := t.TempDir()
	Push(dir, Main, "a")
	Push(dir, Main, "b")
	Pop(dir, Main)
	for _, want := range []string{OpPop, OpPush, OpPush} {
		if e := mustUndo(t, dir); e.Op != want {
			t.Fatalf("undid %q, want %q", e.Op, want)
		}
	}
	if d := (Stack{Stacks: stateOf(t, dir)}).TotalDepth(); d != 0 {
		t.Errorf("depth after full undo = %d, want 0", d)
	}
	if _, _, err := Undo(dir, false); !errors.Is(err, ErrNothingToUndo) {
		t.Errorf("undo on empty log: got %v, want ErrNothingToUndo", err)
	}
}

func TestUndo_PushThatCreatedStackRemovesIt(t *testing.T) {
	dir := t.TempDir()
	Push(dir, "backlog", "idea")
	mustUndo(t, dir)
	if got := (Stack{Stacks: stateOf(t, dir)}).Names(); !reflect.DeepEqual(got, []string{Main}) {
		t.Errorf("names = %v, want [main]", got)
	}
}

func TestUndo_RefusesAfterHandEdit(t *testing.T) {
	dir := t.TempDir()
	Push(dir, Main, "a")
	hand := `{"stacks": [{"name": "main", "topics": ["edited by hand"]}]}`
	if err := os.WriteFile(Path(dir), []byte(hand), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Undo(dir, false); !errors.Is(err, ErrDiverged) {
		t.Fatalf("got %v, want ErrDiverged", err)
	}
	if got := stateOf(t, dir)[0].Topics; len(got) != 1 || got[0].Topic != "edited by hand" {
		t.Errorf("refused undo touched the stack: %+v", got)
	}
	if _, _, err := Undo(dir, true); err != nil {
		t.Fatalf("forced undo: %v", err)
	}
	if got := stateOf(t, dir)[0].Topics; len(got) != 0 {
		t.Errorf("forced undo: main = %+v, want empty", got)
	}
}

func TestCommit_SkipsNoOps(t *testing.T) {
	dir := t.TempDir()
	Push(dir, Main, "a")
	Push(dir, Main, "b")
	if _, err := Move(dir, Main, 1, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := Rename(dir, Main, 1, "b"); err != nil {
		t.Fatal(err)
	}
	entries, err := History(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("log has %d entries, want 2 (no-ops skipped)", len(entries))
	}
}

func TestHistory_RecordsPoppedItemWithRef(t *testing.T) {
	dir := t.TempDir()
	PushItem(dir, Main, Item{Topic: "ship it", Ref: "PR #9"})
	Pop(dir, Main)
	entries, _ := History(dir)
	last := entries[len(entries)-1]
	if last.Op != OpPop || last.Stack != Main || last.Item == nil || *last.Item != (Item{Topic: "ship it", Ref: "PR #9"}) {
		t.Errorf("last entry = %+v", last)
	}
}

func TestHistory_CapsAtMax(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < MaxHistory+5; i++ {
		Push(dir, Main, fmt.Sprintf("t%d", i))
	}
	entries, err := History(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != MaxHistory {
		t.Fatalf("log has %d entries, want %d", len(entries), MaxHistory)
	}
	if got := entries[0].Item.Topic; got != "t5" {
		t.Errorf("oldest kept = %q, want t5", got)
	}
}

func TestTrim_ArchivesOldPopsAndStopsUndoThere(t *testing.T) {
	dir := t.TempDir()
	Push(dir, Main, "done early")
	Pop(dir, Main)
	for i := 0; i < MaxHistory; i++ {
		Push(dir, "backlog", fmt.Sprintf("t%d", i))
	}
	entries, err := History(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != MaxHistory+1 {
		t.Fatalf("log has %d entries, want %d (live tail + archived pop)", len(entries), MaxHistory+1)
	}
	first := entries[0]
	if first.Op != OpPop || first.Item.Topic != "done early" || first.Before != nil || first.AfterHash != "" {
		t.Errorf("archived entry = %+v", first)
	}
	for i := 0; i < MaxHistory; i++ {
		mustUndo(t, dir)
	}
	if _, _, err := Undo(dir, true); !errors.Is(err, ErrNothingToUndo) {
		t.Errorf("undo into archive: got %v, want ErrNothingToUndo", err)
	}
}

func TestTrim_CapsArchivedPops(t *testing.T) {
	var entries []Entry
	for i := 0; i < MaxArchivedPops+3; i++ {
		entries = append(entries, Entry{Op: OpPop, Item: &Item{Topic: fmt.Sprintf("p%d", i)}})
	}
	entries = append(entries, Entry{Op: OpPush, Before: &Stack{}})
	got := trim(entries)
	if len(got) != MaxArchivedPops+1 || got[0].Item.Topic != "p3" {
		t.Errorf("trim kept %d entries, oldest %q", len(got), got[0].Item.Topic)
	}
}

func TestHistory_SkipsUnparseableLines(t *testing.T) {
	dir := t.TempDir()
	Push(dir, Main, "a")
	f, err := os.OpenFile(HistoryPath(dir), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("garbage\n")
	f.Close()
	Push(dir, Main, "b")
	entries, err := History(dir)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("got %d entries, want 2", len(entries))
	}
	mustUndo(t, dir)
	mustUndo(t, dir)
}

func TestHistory_RenameAndRefRecordBothForms(t *testing.T) {
	dir := t.TempDir()
	Push(dir, Main, "old")
	Rename(dir, Main, 1, "new")
	SetRef(dir, Main, 1, "note")
	entries, _ := History(dir)
	rn, rf := entries[1], entries[2]
	if rn.From == nil || rn.From.Topic != "old" || rn.Item.Topic != "new" {
		t.Errorf("rename entry: from %+v item %+v", rn.From, rn.Item)
	}
	if rf.From == nil || rf.From.Ref != "" || rf.Item.Ref != "note" {
		t.Errorf("ref entry: from %+v item %+v", rf.From, rf.Item)
	}
}
