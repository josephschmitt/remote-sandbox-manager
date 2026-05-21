# Spec C — Cross-Sandbox Agent Aggregator (agent view + tmux layout)

## Purpose

A local manager that presents every Claude Code background session across every
Crafting sandbox as one flat list, and lets you attach to any of them. Unlike
specs A and B, this variant does **not** run or supervise agents itself. It
leans entirely on Claude Code’s built-in agent view and its per-user supervisor,
which already host multiple background sessions per machine. The manager’s only
job is the layer Claude Code does not provide: aggregation across machines
(sandboxes).

This is the tmux-layout variant. tmux is invisible plumbing for rendering an
attached session and routing focus. See spec D for the native bubbleterm
variant.

> Relationship to the other specs. This spec assumes Claude Code’s supervisor
> keeps background sessions alive on its own. If that assumption fails inside a
> Crafting workspace, the fallback is graceful, not a return to A/B:
> 
> - **Supervisor survives disconnect** → this spec as written, no dtach.
> - **Supervisor works but dies on SSH disconnect** → this spec plus one
>   persistence anchor per sandbox that keeps the supervisor alive independent of
>   any SSH session. We still read the roster and still let Claude multiplex
>   agents; we do not build a per-agent muxer. Only the anchor is added.
> - **Agent view unusable in the environment** (e.g. blocked under Vertex, or too
>   unstable) → only then fall back to specs A/B’s self-built per-agent dtach.

## The two-level model

- **Within a sandbox:** Claude Code’s supervisor owns everything. Background
  sessions are dispatched with `claude --bg`, listed with `claude agents --json`,
  attached with `claude attach <id>`, and managed with `claude logs|stop|respawn| rm`. Each session is its own Claude Code process; the supervisor keeps them
  alive without a terminal attached. We do not build any of this.
- **Across sandboxes:** this manager. It polls each sandbox’s roster, flattens
  every session into one list keyed by `(sandbox, session-id)`, and attaches on
  selection. There is no cross-sandbox view in Claude Code itself; that gap is
  the whole product.

## Goals

- One flat sidebar of every background session across all sandboxes, with native
  state (working, needs input, idle, completed, failed, stopped).
- Attach to any session in the content pane.
- Dispatch a new session into a chosen sandbox.
- No dtach, no per-agent sockets, no session-id bookkeeping; Claude Code owns
  session lifecycle and identity.
- Sandboxes suspend when no session is working and resume seamlessly.

## Non-goals

- No in-sandbox supervision (Claude Code’s supervisor does it).
- No headless dispatch pipeline (Temporal harness).
- No reuse of the user’s tmux config; no web frontend.

## Architecture overview

```
+-- local machine -------------------------------------------------+
|  tmux server (socket: agentmgr, config: agentmgr.conf)           |
|  +------------+-----------------------------------------------+  |
|  | sidebar    | content pane                                  |  |
|  | (our prog) | cs ssh -t <sb> -- claude attach <id>          |  |
|  | flat list  |                                               |  |
|  | of ALL     |                                               |  |
|  | sessions   |                                               |  |
|  | across     |                                               |  |
|  | sandboxes  |                                               |  |
|  +------------+-----------------------------------------------+  |
|  aggregator: polls cs list + per-sandbox claude agents --json    |
+------------------------------------------------------------------+
        | cs ssh ... claude agents --json   (poll)
        v
+-- sandbox A ----------+   +-- sandbox B ----------+
|  supervisor           |   |  supervisor           |
|   - session 1 (work)  |   |   - session 4 (idle)  |
|   - session 2 (input) |   |   - session 5 (done)  |
|  activity probe reads |   |  activity probe reads |
|   roster -> suspend   |   |   roster -> suspend   |
|  on_resume: respawn   |   |  on_resume: respawn   |
+-----------------------+   +-----------------------+
```

## Aggregation (the core new work)

The spine is the sandbox list from `cs list` (JSON). For each running sandbox,
the aggregator runs `cs ssh <sandbox> -- claude agents --json` (the `--json` flag
is recent; confirm its exact field names against real output) and merges every
returned session into one flat list. Each row is keyed `(sandbox, session-id)`,
where `session-id` is Claude’s short id (its directory name under
`~/.claude/jobs/`).

Polling notes:

- Run per-sandbox queries concurrently with a bounded worker pool; one slow or
  resuming sandbox must not stall the list.
- Cache last-known rosters and render them immediately; refresh on an interval
  (a few seconds) and on demand. Show staleness rather than blocking.
- Skip suspended sandboxes for live polling; show their last-known sessions
  dimmed, and only resume-and-poll when selected.
- A sandbox with no supervisor running returns an empty roster; that’s normal,
  render it as a sandbox with zero sessions.

Row contents come straight from the JSON: name, state, last-activity age, and
pull-request status if present. No hook plumbing is required for the baseline
(see Liveness for the optional push path).

## Attaching and dispatching

**Attach.** Selecting a row respawns the content pane to:

```sh
cs ssh -t <sandbox> -- claude attach <session-id>
```

This takes over the pane exactly as if you’d run `claude` in that sandbox. No
dtach: the session already lives in the sandbox’s supervisor, so attach simply
connects to it, and detaching (killing the local SSH client) leaves it running.

**Dispatch.** A dispatch input in the sidebar starts a new session in a chosen
target sandbox (default: the highlighted row’s sandbox):

```sh
cs ssh <sandbox> -- claude --bg --name "<name>" "<prompt>"
```

`claude --bg` prints a short session id and returns; the session appears in the
next poll. Targeting a sandbox is a v1 simplification; creating a fresh sandbox
on dispatch (via `cs` or Crafting agentic sessions) is a later extension.

## Keep-alive, suspend, and resume

This is the part to validate, because it is where the supervisor is assumed to
replace dtach.

**Keep-alive across disconnect.** The supervisor is a per-user daemon separate
from the terminal, so a session dispatched over `cs ssh` should keep running
after that SSH connection closes. Verify this holds inside a Crafting workspace.

If that test fails and sessions die on disconnect, the remedy is narrow: anchor
the supervisor, not each agent. One per-sandbox holder keeps the supervisor
process alive independent of SSH, after which everything else in this spec is
unchanged. The cleanest anchor is a Crafting workspace daemon / automation entry,
since Crafting then owns the lifecycle and it composes with the activity probe; a
single dtach-held process is the lighter-weight alternative. Either way it is one
anchor per sandbox, not one per agent, so Claude Code still does all the
multiplexing and we still read the roster.

**Activity probe.** A custom activity probe (workspace definition) reports the
sandbox active when any session is working, reading Claude’s own state rather
than hand-maintained flags:

```sh
# agent-probe.sh — verify --json field names and the exit-code convention
active_exit=0; inactive_exit=1
json=$(claude agents --json 2>/dev/null) || exit "$inactive_exit"
echo "$json" | jq -e '[.sessions[]? | select(.state=="working")] | length > 0' \
  >/dev/null 2>&1 && exit "$active_exit"
exit "$inactive_exit"
```

Only the working state keeps the sandbox awake. Needs-input is a human wait, not
compute, so it is idle-eligible; an attached user responding keeps the box alive
independently via the SSH connection. Decide separately how to treat a `/loop`
session sleeping between iterations (let it suspend and rely on its schedule, or
keep alive).

**Suspend and resume.** Background sessions do not survive sleep, which is what a
Crafting suspend is, so on suspend they stop and show failed. The `on_resume`
lifecycle hook restores them:

```yaml
on_resume:
  run:
    cmd: claude respawn --all
```

`respawn` restarts stopped sessions with their conversation intact. `cs ssh` to a
suspended sandbox triggers the resume transition and blocks until Running, after
which `claude attach <id>` lands on the respawned session and Claude posts a
recap. Show a transient “resuming…” state in the sidebar, gated by Readiness /
Wait-For.

Note the supervisor’s own idle behavior is complementary: it stops a finished
session’s process after about an hour and restarts it on next access. The probe
only cares about the working state, so this does not interfere.

## Local layer (tmux)

Identical to spec A’s local layer; summarized here.

```sh
TMUX= tmux -L agentmgr -f ./agentmgr.conf \
  new-session -s mgr -n main \; split-window -h \; select-pane -t 0
```

```tmux
set  -g status off
set  -g mouse on
set  -g escape-time 0
set  -g focus-events on
set  -g window-size latest
setw -g aggressive-resize on
bind -n F12 select-pane -t :.+      # the only key tmux intercepts
```

The sidebar program runs in the left pane: it owns the aggregator, renders the
flat list, handles its own `j`/`k`/`enter` navigation, and on selection respawns
the content pane:

```sh
tmux -L agentmgr respawn-pane -t mgr:main.1 -k -- \
  ~/.agentmgr/attach.sh <sandbox> <session-id>
```

Reconnect wrapper:

```sh
#!/usr/bin/env bash
sandbox="$1"; id="$2"
while true; do
  cs ssh -t "$sandbox" -- claude attach "$id"
  # respawn -k kills this tree on an intentional switch, so reaching here means
  # an unexpected SSH drop; the session still lives in the supervisor, reattach.
  sleep 1
done
```

`mouse on` keeps scrollback and click-to-focus. `F12` is the only intercepted
key; everything else reaches the active pane.

## Liveness (“which session needs me”)

Baseline comes free from the aggregator: `claude agents --json` already carries
the state. Map states to sidebar markers directly. For lower latency on the
needs-input edge, optionally add Claude Code `Notification`/`Stop` hooks that
POST to a small local listener over `cs portforward` or a Crafting endpoint, and
treat that as a push hint that triggers an immediate re-poll of that sandbox.
Hybrid: poll for the baseline, push for the edges.

## Open questions / risks to verify first

- **Supervisor survives disconnect (central assumption).** Confirm a session
  dispatched via `cs ssh <sb> -- claude --bg …` keeps running after the SSH
  connection closes, and is still there on the next `cs ssh … claude agents --json`. If it dies, add a single per-sandbox persistence anchor (workspace
  daemon or one dtach-held process) rather than abandoning the approach; only an
  unusable agent view sends you to specs A/B. **Test this first.**
- **Suspend/respawn round trip.** Confirm a Crafting suspend stops the
  supervisor and sessions, and that `claude respawn --all` in `on_resume`
  restores them with conversation intact.
- **`claude agents --json` shape.** Newly shipped; confirm field names (session
  id, name, state values, cwd, last-activity, PR status) and that it works
  non-interactively over `cs ssh`.
- **Probe exit-code convention.** Docs are ambiguous; confirm with a one-line
  probe before relying on it.
- **Agent view through Vertex AI.** Earlier versions didn’t open agent view in
  Bedrock/Vertex/Foundry environments. The Compass setup runs Claude through
  Vertex, so verify availability there even if personal Max-plan use is fine.
- **Polling cost.** Many sandboxes times per-poll `cs ssh` calls adds up; bound
  concurrency, cache, and back off on suspended or slow sandboxes.
- **Quota fan-out.** Parallel sessions consume subscription usage linearly;
  surface a running count so wide fan-out is a deliberate choice.

## POC milestones

1. **Supervisor survival test (before anything else).** In one sandbox: `cs ssh <sb> -- claude --bg "…"`, disconnect, reconnect, `claude agents --json`,
   confirm it’s still running. If it died, retry with a persistence anchor (a
   workspace daemon, or a dtach-held supervisor) and confirm survival. Then
   suspend the sandbox, resume, run `claude respawn --all`, confirm the
   conversation is intact.
1. Parse `claude agents --json` over `cs ssh`; build the flat aggregated list
   across two sandboxes with concurrent polling and caching.
1. Add the tmux two-pane layout, F12 toggle, and attach-on-select.
1. Add the custom activity probe (working-state) and the `on_resume` respawn
   hook; verify suspend-when-idle and seamless resume.
1. Add the dispatch input and optional hook-based liveness push.