#!/usr/bin/env bash
# launch.sh — start the agentmgr tmux layout. Lives on its own socket
# (-L agentmgr) with its own config (-f agentmgr.conf), so the user's tmux
# is untouched.
#
# Usage:
#   ./launch.sh           # mock backend (no Crafting access needed)
#   ./launch.sh --real    # real Crafting backend (requires `cs` and `claude`)
#
# Idempotent: if an agentmgr server is already running, attaches to it.

set -euo pipefail

# Resolve paths so the script works regardless of cwd.
here="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" >/dev/null && pwd)"
root="$(cd -- "$here/.." >/dev/null && pwd)"
conf="$here/agentmgr.conf"
bin="$root/agentmgr"

# Backend flag plumbed to the sidebar and the attach helper.
backend_flag=""
if [[ "${1:-}" == "--real" ]]; then
  backend_flag="--real"
fi
# Also honor an env var so subprocesses launched via tmux see the same choice.
if [[ -n "$backend_flag" ]]; then
  export AGENTMGR_BACKEND="crafting"
else
  export AGENTMGR_BACKEND="mock"
fi

# Build if the binary is missing or stale.
if [[ ! -x "$bin" || "$root/main.go" -nt "$bin" ]]; then
  ( cd "$root" && go build -o "$bin" . )
fi

# A clean placeholder so the right pane has content before the user picks a
# session. Replaced by `tmux respawn-pane` when a row is selected.
placeholder="$bin attach --placeholder"

# Single tmux session named "mgr" with one window "main", split horizontally:
# left pane = sidebar (our Bubble Tea program), right pane = content
# (placeholder until selection, then `cs ssh -t <sb> -- claude attach <id>`).
if ! TMUX= tmux -L agentmgr has-session -t mgr 2>/dev/null; then
  TMUX= tmux -L agentmgr -f "$conf" new-session -d -s mgr -n main \
    "$bin sidebar $backend_flag"
  tmux -L agentmgr split-window -h -t mgr:main -l 70% "$placeholder"
  tmux -L agentmgr select-pane -t mgr:main.0
fi

exec tmux -L agentmgr attach -t mgr
