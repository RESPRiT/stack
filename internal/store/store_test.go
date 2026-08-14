package store

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestItemJSON_BareString(t *testing.T) {
	// Ref-less item round-trips as a bare JSON string.
	in := Item{Topic: "fix the bug"}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got, want := string(b), `"fix the bug"`; got != want {
		t.Errorf("marshal: got %s, want %s", got, want)
	}
	var out Item
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out != in {
		t.Errorf("round-trip: got %+v, want %+v", out, in)
	}
}

func TestItemJSON_WithRef(t *testing.T) {
	// Item with ref round-trips as an object.
	in := Item{Topic: "ship feature", Ref: "_TODO/ship-feature.md"}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"topic":"ship feature","ref":"_TODO/ship-feature.md"}`
	if got := string(b); got != want {
		t.Errorf("marshal: got %s, want %s", got, want)
	}
	var out Item
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out != in {
		t.Errorf("round-trip: got %+v, want %+v", out, in)
	}
}

func TestItemJSON_MixedArray(t *testing.T) {
	// A topics array with a mix of bare strings and objects loads and
	// saves preserving each entry's shape.
	raw := `[
    "topic A",
    {"topic": "topic B", "ref": "with ref"},
    "topic C"
  ]`
	var topics []Item
	if err := json.Unmarshal([]byte(raw), &topics); err != nil {
		t.Fatalf("unmarshal topics: %v", err)
	}
	if len(topics) != 3 {
		t.Fatalf("topics: got %d, want 3", len(topics))
	}
	if topics[0] != (Item{Topic: "topic A"}) {
		t.Errorf("topics[0]: got %+v", topics[0])
	}
	if topics[1] != (Item{Topic: "topic B", Ref: "with ref"}) {
		t.Errorf("topics[1]: got %+v", topics[1])
	}
	if topics[2] != (Item{Topic: "topic C"}) {
		t.Errorf("topics[2]: got %+v", topics[2])
	}
	// Re-marshal and check that bare strings stay bare and objects stay objects.
	out, err := json.Marshal(topics)
	if err != nil {
		t.Fatalf("re-marshal topics: %v", err)
	}
	str := string(out)
	if !strings.Contains(str, `"topic A"`) || !strings.Contains(str, `"topic C"`) {
		t.Errorf("expected bare strings preserved: %s", str)
	}
	if !strings.Contains(str, `"topic":"topic B"`) || !strings.Contains(str, `"ref":"with ref"`) {
		t.Errorf("expected object preserved: %s", str)
	}
}

func TestLoad_MigratesLegacyTopics(t *testing.T) {
	// A legacy {"topics": [...]} file loads as main's topics.
	dir := t.TempDir()
	legacy := `{
  "topics": ["bottom", {"topic": "top", "ref": "r"}],
  "updated_at": "2026-05-19T22:00:00Z"
}`
	if err := os.WriteFile(Path(dir), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := s.Names(); len(got) != 1 || got[0] != Main {
		t.Fatalf("names: got %v, want [main]", got)
	}
	topics, _ := s.Get(Main)
	if len(topics) != 2 || topics[1] != (Item{Topic: "top", Ref: "r"}) {
		t.Errorf("main topics: got %+v", topics)
	}
}

func TestLoad_MainAlwaysFirst(t *testing.T) {
	// normalize keeps main at index 0 even if the file orders it later,
	// and synthesizes it when absent.
	dir := t.TempDir()
	raw := `{"stacks": [{"name": "backlog", "topics": ["idea"]}, {"name": "main", "topics": ["work"]}]}`
	if err := os.WriteFile(Path(dir), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := s.Names(); got[0] != Main || got[1] != "backlog" {
		t.Errorf("names: got %v, want [main backlog]", got)
	}
}

func TestPushPopNamedStacks(t *testing.T) {
	dir := t.TempDir()
	if _, err := Push(dir, Main, "feature work"); err != nil {
		t.Fatal(err)
	}
	// Lazy creation of a named stack on first push.
	s, err := Push(dir, "backlog", "an idea")
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Names(); len(got) != 2 || got[1] != "backlog" {
		t.Fatalf("names: got %v", got)
	}
	// Pop targets the named stack, not main.
	popped, after, err := Pop(dir, "backlog")
	if err != nil {
		t.Fatal(err)
	}
	if popped.Topic != "an idea" || len(after) != 0 {
		t.Errorf("pop backlog: got %+v, after %d", popped, len(after))
	}
	// Emptied stacks persist.
	s, _ = Load(dir)
	if got := s.Names(); len(got) != 2 {
		t.Errorf("emptied stack should persist: %v", got)
	}
	// Pop on an unknown stack errors.
	if _, _, err := Pop(dir, "nope"); !errors.Is(err, ErrNoStack) {
		t.Errorf("pop unknown: got %v, want ErrNoStack", err)
	}
	// Delete removes; main is protected.
	if n, err := Delete(dir, "backlog"); err != nil || n != 0 {
		t.Errorf("delete: n=%d err=%v", n, err)
	}
	if _, err := Delete(dir, Main); err == nil {
		t.Error("delete main should fail")
	}
}

func TestMoveItem(t *testing.T) {
	dir := t.TempDir()
	if _, err := Push(dir, Main, "focus work"); err != nil {
		t.Fatal(err)
	}
	if _, err := Push(dir, "backlog", "parked idea"); err != nil {
		t.Fatal(err)
	}
	if _, err := SetRef(dir, "backlog", 1, "the ref"); err != nil {
		t.Fatal(err)
	}
	// Promote backlog's top onto main: ref travels, lands on top.
	item, s, err := MoveItem(dir, "backlog", 1, Main)
	if err != nil {
		t.Fatal(err)
	}
	if item != (Item{Topic: "parked idea", Ref: "the ref"}) {
		t.Errorf("moved item: got %+v", item)
	}
	topics, _ := s.Get(Main)
	if topics[len(topics)-1] != item {
		t.Errorf("main top: got %+v, want moved item", topics[len(topics)-1])
	}
	if s.Depth("backlog") != 0 {
		t.Errorf("backlog should be empty, depth %d", s.Depth("backlog"))
	}
	// Same-stack move is an error; unknown source is an error.
	if _, _, err := MoveItem(dir, Main, 1, Main); err == nil {
		t.Error("same-stack move should fail")
	}
	if _, _, err := MoveItem(dir, "nope", 1, Main); !errors.Is(err, ErrNoStack) {
		t.Errorf("unknown source: got %v, want ErrNoStack", err)
	}
	// Destination is created lazily.
	if _, s, err = MoveItem(dir, Main, 1, "later"); err != nil {
		t.Fatal(err)
	}
	if s.Depth("later") != 1 {
		t.Errorf("later should hold the moved item, depth %d", s.Depth("later"))
	}
}

func TestItemJSON_RejectsGarbage(t *testing.T) {
	var i Item
	if err := json.Unmarshal([]byte(`42`), &i); err == nil {
		t.Error("expected error on numeric input, got nil")
	}
}
