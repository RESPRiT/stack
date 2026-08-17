package banner

import (
	"strings"
	"testing"
)

// lines splits a rendered banner for per-row assertions.
func lines(t *testing.T, f Focus, width int) []string {
	t.Helper()
	return strings.Split(Render(f, width), "\n")
}

func TestRenderRuleAndCounterAlign(t *testing.T) {
	got := lines(t, Focus{Stack: "main", Depth: 3, Topic: "Wire up retry budget"}, 40)
	if len(got) != 3 {
		t.Fatalf("want 3 lines, got %d: %q", len(got), got)
	}

	rule := strings.Trim(got[0], "`")
	if n := len([]rune(rule)); n != 40 {
		t.Errorf("rule is %d runes, want 40", n)
	}
	if strings.Trim(rule, barChar) != "" {
		t.Errorf("rule has non-bar runes: %q", rule)
	}

	// The counter must end flush with the rule, so both inline-code spans
	// are the same rendered width.
	counterRow := strings.Trim(got[1], "`")
	if n := len([]rune(counterRow)); n != 40 {
		t.Errorf("counter row is %d runes, want 40 to match the rule", n)
	}
	if !strings.HasSuffix(counterRow, "[main · depth 3]") {
		t.Errorf("counter row = %q, want it to end with the counter", counterRow)
	}

	if got[2] != "`▶` **Wire up retry budget**" {
		t.Errorf("topic row = %q", got[2])
	}
}

func TestRenderEmptyStack(t *testing.T) {
	got := lines(t, Focus{Stack: "main"}, 40)
	if !strings.Contains(got[1], "[main · empty]") {
		t.Errorf("counter row = %q, want an empty marker not a depth", got[1])
	}
	if !strings.Contains(got[2], "nothing on the stack") {
		t.Errorf("topic row = %q, want the empty placeholder", got[2])
	}
}

// Only main's top is the session focus, so only main gets ▶ — the same
// rule `stack ls` applies when it renders named stacks without a marker.
func TestRenderNamedStackDropsFocusMarker(t *testing.T) {
	got := lines(t, Focus{Stack: "backlog", Depth: 1, Topic: "An idea"}, 40)
	if strings.Contains(got[2], "▶") {
		t.Errorf("named stack claimed the focus marker: %q", got[2])
	}
	if got[2] != "`·` **An idea**" {
		t.Errorf("topic row = %q", got[2])
	}
}

func TestRenderSingleLineRef(t *testing.T) {
	got := Render(Focus{Stack: "main", Depth: 2, Topic: "Ship it", Ref: "blocked on PR #123"}, 40)
	if !strings.HasSuffix(got, "\n\n> **Ref:** blocked on PR #123") {
		t.Errorf("ref not quoted as expected:\n%s", got)
	}
}

// A ref with a blank line in it must stay one contiguous blockquote —
// an unprefixed gap splits it into two quotes when rendered.
func TestRenderMultiLineRefKeepsOneQuote(t *testing.T) {
	ref := "waiting on review\n\nsee internal/store/store.go"
	got := lines(t, Focus{Stack: "main", Depth: 1, Topic: "Ship it", Ref: ref}, 40)

	quoted := got[len(got)-3:]
	want := []string{"> **Ref:** waiting on review", ">", "> see internal/store/store.go"}
	for i := range want {
		if quoted[i] != want[i] {
			t.Errorf("quote line %d = %q, want %q", i, quoted[i], want[i])
		}
	}
}

func TestResolveWidthClamps(t *testing.T) {
	for _, tc := range []struct {
		override, want int
	}{
		{5, minWidth},
		{500, maxWidth},
		{40, 40},
	} {
		if got := resolveWidth(tc.override); got != tc.want {
			t.Errorf("resolveWidth(%d) = %d, want %d", tc.override, got, tc.want)
		}
	}
}

// A pane too narrow to fit the counter still renders a rule the counter
// can align against, rather than a stub the label overflows.
func TestNarrowPaneCounterFitsRule(t *testing.T) {
	got := lines(t, Focus{Stack: "main", Depth: 1, Topic: "x"}, 1)
	rule := len([]rune(strings.Trim(got[0], "`")))
	counterRow := len([]rune(strings.Trim(got[1], "`")))
	if rule != minWidth {
		t.Errorf("rule width = %d, want the %d floor", rule, minWidth)
	}
	if counterRow > rule {
		t.Errorf("counter row (%d) overflows the rule (%d)", counterRow, rule)
	}
}
