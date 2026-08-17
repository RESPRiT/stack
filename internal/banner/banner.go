// Package banner renders the context-switch header a coding agent echoes
// when a stack's top changes.
//
// The header is markdown bound for the agent's reply, not shell output:
// the rule and its counter are wrapped in backticks so they render as a
// monospace band inside the agent's message, and the topic is bold. It
// exists to make a context switch legible at a glance — after a push, a
// pop, or a cross-stack move, the reader should be able to see where the
// session's attention landed without parsing a status line.
//
// Rendering lives in the binary rather than in the skill prompts on
// purpose. An agent asked to draw its own rule gets the width, the
// character, or the backticks wrong often enough that the header stops
// reading as a landmark, which is the only thing it is for. The skills
// echo what `stack focus` prints and write prose underneath it.
package banner

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

const barChar = "━"

// defaultWidth is the rule width used when the pane can't be measured —
// outside tmux, or when tmux declines to answer.
const defaultWidth = 60

// minWidth keeps a narrow pane from collapsing the rule into a stub
// shorter than the counter it right-aligns against. maxWidth keeps a wide
// terminal from rendering a full-width wall of ━ that reads as noise
// rather than as a separator.
const (
	minWidth = 20
	maxWidth = 100
)

// gutterCols is the left gutter the agent's rendered message sits behind,
// subtracted from the pane width so the rule ends inside the window
// instead of wrapping onto a second line.
const gutterCols = 2

// Focus is the state a banner describes: which stack's top is being
// announced, its depth, and the item now sitting on top. An empty Topic
// means the stack has nothing left on it.
type Focus struct {
	Stack string
	Depth int
	Topic string
	Ref   string
}

// Render returns the markdown header for f. A width of 0 measures the
// caller's tmux pane; a positive width overrides the measurement.
func Render(f Focus, width int) string {
	w := resolveWidth(width)
	var b strings.Builder

	b.WriteString("`" + strings.Repeat(barChar, w) + "`\n")
	b.WriteString("`" + rightAlign(counter(f), w) + "`\n")

	// Only main's top is the session's focus, so only main earns ▶ —
	// same rule `stack ls` follows.
	marker := "·"
	if f.Stack == "main" {
		marker = "▶"
	}
	if f.Topic == "" {
		b.WriteString("`" + marker + "` *(nothing on the stack)*")
		return b.String()
	}
	b.WriteString("`" + marker + "` **" + f.Topic + "**")
	if f.Ref != "" {
		b.WriteString("\n\n" + quoteRef(f.Ref))
	}
	return b.String()
}

func counter(f Focus) string {
	if f.Depth == 0 {
		return fmt.Sprintf("[%s · empty]", f.Stack)
	}
	return fmt.Sprintf("[%s · depth %d]", f.Stack, f.Depth)
}

// rightAlign pads s to sit flush against the right end of a rule of the
// given width. Padding counts runes, not bytes, so the multi-byte
// separator in the counter doesn't shove the label off the end.
func rightAlign(s string, width int) string {
	pad := width - len([]rune(s))
	if pad < 0 {
		pad = 0
	}
	return strings.Repeat(" ", pad) + s
}

// quoteRef renders a ref as a markdown blockquote. Every line gets the
// prefix, blank lines included, so a multi-line handoff note stays one
// contiguous quote instead of splitting into two at the first gap.
func quoteRef(ref string) string {
	lines := strings.Split(strings.TrimRight(ref, "\n"), "\n")
	for i, line := range lines {
		if i == 0 {
			lines[i] = "> **Ref:** " + line
			continue
		}
		lines[i] = strings.TrimRight("> "+line, " ")
	}
	return strings.Join(lines, "\n")
}

// resolveWidth picks the rule width from an explicit override or a pane
// measurement. The override replaces the measurement but not the clamp,
// so a wildly out-of-range value still renders a usable rule.
func resolveWidth(override int) int {
	w := defaultWidth
	switch {
	case override > 0:
		w = override
	default:
		if measured, ok := paneWidth(); ok {
			w = measured - gutterCols
		}
	}
	if w < minWidth {
		return minWidth
	}
	if w > maxWidth {
		return maxWidth
	}
	return w
}

// paneWidth reports the width, in columns, of the tmux pane this process
// was launched from.
//
// stack runs as a child of the agent, whose stdout is a pipe rather than
// a terminal, so there is no window size to read off the file descriptor
// and $COLUMNS arrives empty. tmux is the only source that knows the real
// size. $TMUX_PANE names the pane the caller occupies; without it tmux
// answers for the session's active pane, which is the same pane in the
// common case.
//
// The second return is false whenever the measurement isn't trustworthy —
// outside tmux, tmux missing, or an unparseable reply — leaving the
// caller to fall back rather than act on a guess.
func paneWidth() (int, bool) {
	if os.Getenv("TMUX") == "" {
		return 0, false
	}
	args := []string{"display-message", "-p"}
	if pane := os.Getenv("TMUX_PANE"); pane != "" {
		args = append(args, "-t", pane)
	}
	args = append(args, "#{pane_width}")

	out, err := exec.Command("tmux", args...).Output()
	if err != nil {
		return 0, false
	}
	w, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || w <= 0 {
		return 0, false
	}
	return w, true
}
