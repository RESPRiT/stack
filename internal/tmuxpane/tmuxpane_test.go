package tmuxpane

import "testing"

func TestDecide(t *testing.T) {
	cases := []struct {
		name string
		cols int
		rows int
		want placement
	}{
		// Visually tall — top.
		{"tall", 60, 60, placementTop},
		// Visually wide — right.
		{"wide", 200, 60, placementRight},
		// Exactly visually square — right (>= threshold).
		{"square", 120, 60, placementRight},
		// Just under visually square — top.
		{"just under square", 119, 60, placementTop},
		// Zero rows is a safe default to top.
		{"zero rows", 100, 0, placementTop},
		// Below the min-width threshold, force top even if aspect favors right.
		// 63 cols × 8 rows → aspect 3.94 would normally be right, but cols < 64.
		{"below min-width forces top", 63, 8, placementTop},
		// At the min-width threshold, right placement is admissible (and aspect agrees).
		{"at min-width with right aspect", minWindowColsForRight, 30, placementRight},
	}
	for _, c := range cases {
		got := decide(c.cols, c.rows)
		if got != c.want {
			t.Errorf("%s: decide(%d, %d) = %v, want %v",
				c.name, c.cols, c.rows, got, c.want)
		}
	}
}

func TestRightWidthFor(t *testing.T) {
	cases := []struct {
		name    string
		winCols int
		want    int
	}{
		// At or above 4 × rightCols, the cap matches rightCols.
		{"wide window stays at rightCols", rightCols*4 + 100, rightCols},
		{"exactly 4×rightCols stays at rightCols", rightCols * 4, rightCols},
		// Below 4 × rightCols, the cap kicks in: width = winCols / 4.
		{"100 cols clamps to 25", 100, 25},
		{"80 cols clamps to 20", 80, 20},
		// At the minWindowColsForRight floor, returns minRightWidth.
		{"at min-window floor returns min-right-width",
			minWindowColsForRight, minRightWidth},
		// Zero / unknown winCols falls back to rightCols.
		{"unknown winCols falls back", 0, rightCols},
	}
	for _, c := range cases {
		got := rightWidthFor(c.winCols)
		if got != c.want {
			t.Errorf("%s: rightWidthFor(%d) = %d, want %d",
				c.name, c.winCols, got, c.want)
		}
	}
}

func TestParseHookState(t *testing.T) {
	const bin = "/Users/h/go/bin/stack"
	cases := []struct {
		name      string
		out       string
		installed bool
		dupes     []string
		stale     []string
	}{
		{
			name: "fresh server",
			out:  "",
		},
		{
			name:      "installed, nothing stale",
			out:       `window-resized[0] run-shell "/Users/h/go/bin/stack tui reflow #{window_id}"` + "\n",
			installed: true,
		},
		{
			name:  "legacy client-resized in both historical forms",
			out:   "client-resized[0] run-shell \"/Users/h/go/bin/stack reflow\"\nclient-resized[1] run-shell \"/Users/h/go/bin/stack tui reflow\"\n",
			stale: []string{"client-resized[0]", "client-resized[1]"},
		},
		{
			name:      "installed alongside a legacy hook",
			out:       "client-resized[2] run-shell \"/Users/h/go/bin/stack tui reflow\"\nwindow-resized[0] run-shell \"/Users/h/go/bin/stack tui reflow #{window_id}\"\n",
			installed: true,
			stale:     []string{"client-resized[2]"},
		},
		{
			name:      "duplicate installs collapse to the first slot",
			out:       "window-resized[0] run-shell \"/Users/h/go/bin/stack tui reflow #{window_id}\"\nwindow-resized[1] run-shell \"/Users/h/go/bin/stack tui reflow #{window_id}\"\nwindow-resized[2] run-shell \"/Users/h/go/bin/stack tui reflow #{window_id}\"\n",
			installed: true,
			dupes:     []string{"window-resized[1]", "window-resized[2]"},
		},
		{
			name: "someone else's hooks are left alone",
			out:  "client-resized[0] run-shell \"/opt/other/stack reflow\"\nwindow-resized[0] refresh-client\nwindow-resized[1] run-shell \"/opt/other/stack tui reflow\"\n",
		},
	}
	for _, c := range cases {
		st := parseHookState(c.out, bin)
		if st.installed != c.installed {
			t.Errorf("%s: installed = %v, want %v", c.name, st.installed, c.installed)
		}
		if !equalStrings(st.stale, c.stale) {
			t.Errorf("%s: stale = %v, want %v", c.name, st.stale, c.stale)
		}
		if !equalStrings(st.dupes, c.dupes) {
			t.Errorf("%s: dupes = %v, want %v", c.name, st.dupes, c.dupes)
		}
	}
}

func TestReflowHookCommand(t *testing.T) {
	got := reflowHookCommand("/Users/h/go/bin/stack")
	want := `run-shell '/Users/h/go/bin/stack tui reflow #{window_id}'`
	if got != want {
		t.Errorf("reflowHookCommand = %q, want %q", got, want)
	}
	// The installed command must round-trip through parseHookState's
	// detection once tmux re-prints it double-quoted.
	st := parseHookState("window-resized[0] run-shell \"/Users/h/go/bin/stack tui reflow #{window_id}\"\n", "/Users/h/go/bin/stack")
	if !st.installed {
		t.Errorf("installed hook not detected by parseHookState")
	}
}

func TestReflowStateName(t *testing.T) {
	cases := map[[2]string]string{
		{"@12", "lock"}:    "reflow-12.lock",
		{"@0", "trailing"}: "reflow-0.trailing",
		{"7", "lock"}:      "reflow-7.lock",
	}
	for in, want := range cases {
		if got := reflowStateName(in[0], in[1]); got != want {
			t.Errorf("reflowStateName(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestCallerTarget(t *testing.T) {
	t.Setenv("TMUX_PANE", "%7")
	if got := callerTarget(); len(got) != 2 || got[0] != "-t" || got[1] != "%7" {
		t.Errorf("callerTarget with TMUX_PANE=%%7 = %v, want [-t %%7]", got)
	}
	t.Setenv("TMUX_PANE", "")
	if got := callerTarget(); got != nil {
		t.Errorf("callerTarget without TMUX_PANE = %v, want nil", got)
	}
}
