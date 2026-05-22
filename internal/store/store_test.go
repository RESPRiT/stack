package store

import (
	"encoding/json"
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
	// A Stack-shaped JSON with a mix of bare strings and objects loads
	// and saves preserving each entry's shape.
	raw := `{
  "topics": [
    "topic A",
    {"topic": "topic B", "ref": "with ref"},
    "topic C"
  ],
  "updated_at": "2026-05-19T22:00:00Z"
}`
	var s Stack
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatalf("unmarshal stack: %v", err)
	}
	if len(s.Topics) != 3 {
		t.Fatalf("topics: got %d, want 3", len(s.Topics))
	}
	if s.Topics[0] != (Item{Topic: "topic A"}) {
		t.Errorf("topics[0]: got %+v", s.Topics[0])
	}
	if s.Topics[1] != (Item{Topic: "topic B", Ref: "with ref"}) {
		t.Errorf("topics[1]: got %+v", s.Topics[1])
	}
	if s.Topics[2] != (Item{Topic: "topic C"}) {
		t.Errorf("topics[2]: got %+v", s.Topics[2])
	}
	// Re-marshal and check that bare strings stay bare and objects stay objects.
	out, err := json.Marshal(s.Topics)
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

func TestItemJSON_RejectsGarbage(t *testing.T) {
	var i Item
	if err := json.Unmarshal([]byte(`42`), &i); err == nil {
		t.Error("expected error on numeric input, got nil")
	}
}
