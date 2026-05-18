.PHONY: build hmr clean

build:
	go build -o bin/stack .

# Rebuild + respawn the TUI in a target tmux pane on every .go change.
# Usage: make hmr PANE=%103 [SESS=demo]
hmr:
	@test -n "$(PANE)" || (echo "make hmr PANE=<tmux-pane-id> [SESS=demo]" >&2; exit 1)
	STACK_HMR_PANE=$(PANE) CLAUDE_SESSION_ID=$(or $(SESS),demo) ./scripts/hmr.sh

clean:
	rm -rf bin
