# Spec D — Cross-Sandbox Agent Aggregator (agent view + bubbleterm)

## Purpose

A local manager that presents every Claude Code background session across every
Crafting sandbox as one flat list, and lets you attach to any of them. Like spec
C, it does **not** supervise agents itself; it leans on Claude Code’s built-in
agent view and per-user supervisor for all in-sandbox session hosting, and only
adds the cross-sandbox aggregation Claude Code lacks.

This is the native variant: a single Go binary that owns all chrome and embeds a
terminal emulator to render the attached session. No tmux. See spec C for the
tmux-layout variant.

> Relationship to the other specs. This spec assumes Claude Code’s supervisor
> keeps background sessions alive on its own. If that fails inside a Crafting
> workspace, the fallback is graceful, not a return to A/B:
> 
> - **Supervisor survives disconnect** → this spec as written, no dtach.
> - **Supervisor works but dies on SSH disconnect** → this spec plus one
>   persistence anchor per sandbox that keeps the supervisor alive independent of
>   SSH. We still read the roster and let Claude multiplex agents; no per-agent
>   muxer. Only the anchor is added.
> - **Agent view unusable in the environment** (blocked under Vertex, or too
>   unstable) → only then fall back to specs A/B’s self-built per-agent dtach.

## The two-level model

- **Within a sandbox:** Claude Code’s supervisor owns session lifecycle.
  Dispatch with `claude --bg`, list with `claude agents --json`, attach with
  `claude attach <id>`, manage with `claude logs|stop|respawn|rm`. Each session
  is its own process kept alive by the supervisor without a terminal.
- **Across sandboxes:** this manager. It polls each sandbox’s roster, flattens
  every session into one list keyed by `(sandbox, session-id)`, and attaches on
  selection. The cross-sandbox view is the whole product.

## Goals

- One flat sidebar of every background session across all sandboxes, with native
  state from Claude Code.
- Total control of the frame: the flat list, the content pane, and any inline UI
  (PR badges, per-session metadata) are ours to draw.
- One embedded terminal for the attached session.
- Dispatch a new session into a chosen sandbox.
- No dtach, no per-agent sockets, no session-id bookkeeping.

## Non-goals

- No in-sandbox supervision, no headless pipeline, no tmux, no web frontend.

## Architecture overview

```
+-- local machine -------------------------------------------------+
|  agentmgr (single Go binary, Bubble Tea v2)                      |
|  +------------+-----------------------------------------------+  |
|  | flat list  | bubbleterm.Model (emulator widget)            |  |
|  | of ALL     |   ^ cells           keys v                    |  |
|  | sessions   |   +-- local PTY (creack/pty) --------------+  |  |
|  | across     |       cs ssh -t <sb> -- claude attach <id> |  |  |
|  | sandboxes  |                                            |  |  |
|  +------------+-----------------------------------------------+  |
|  aggregator goroutine: cs list + per-sandbox claude agents --json|
+------------------------------------------------------------------+
        | cs ssh ... claude agents --json   (poll)
        v
   sandbox A supervisor (sessions...)   sandbox B supervisor (...)
   probe reads roster -> suspend        on_resume: claude respawn --all
```

## Aggregation (the core new work)

Identical to spec C. The spine is `cs list` (JSON). For each running sandbox, a
worker runs `cs ssh <sandbox> -- claude agents --json` (flag is recent; confirm
field names) and merges sessions into one flat list keyed `(sandbox, session-id)`. Poll concurrently with a bounded pool, cache and render
last-known immediately, refresh on an interval, skip suspended sandboxes for
live polling and show their sessions dimmed until selected. Row contents (name,
state, age, PR status) come straight from the JSON.

## Attaching and dispatching

**Attach.** Selecting a row lazily starts:

```sh
cs ssh -t <sandbox> -- claude attach <session-id>
```

under a local PTY, and renders it in the embedded emulator. No dtach; the
session lives in the supervisor, so closing the local client detaches without
stopping it.

**Dispatch.** A dispatch input starts a session in the highlighted row’s sandbox:

```sh
cs ssh <sandbox> -- claude --bg --name "<name>" "<prompt>"
```

It appears in the next poll. Per-sandbox targeting is the v1 simplification.

## Keep-alive, suspend, and resume

Identical to spec C; this is the part to validate.

- **Keep-alive:** the supervisor (a per-user daemon, separate from the terminal)
  should keep a `claude --bg` session alive after the `cs ssh` that started it
  disconnects. Verify inside a Crafting workspace. If it dies on disconnect, the
  fix is one per-sandbox persistence anchor (a Crafting workspace daemon, or a
  single dtach-held process) that keeps the supervisor up; the rest of the spec
  is unchanged, and it stays one anchor per sandbox, not per agent.
- **Activity probe:** a custom workspace probe reports active when any session is
  working, reading Claude’s state:

```sh
# agent-probe.sh — verify --json field names and exit-code convention
active_exit=0; inactive_exit=1
json=$(claude agents --json 2>/dev/null) || exit "$inactive_exit"
echo "$json" | jq -e '[.sessions[]? | select(.state=="working")] | length > 0' \
  >/dev/null 2>&1 && exit "$active_exit"
exit "$inactive_exit"
```

Only the working state keeps the box awake; needs-input is a human wait and is
idle-eligible.

- **Suspend/resume:** a Crafting suspend is a sleep, so sessions stop and show
  failed. The `on_resume` lifecycle hook runs `claude respawn --all`, which
  restarts stopped sessions with conversation intact. `cs ssh` to a suspended
  sandbox blocks through resume, then `claude attach <id>` lands on the
  respawned session; show “resuming…” gated by Readiness / Wait-For.

## Local layer (bubbleterm + creack/pty)

Identical mechanics to spec B; the only change is the attach command.

### Libraries

- `charm.land/bubbletea/v2`, `lipgloss/v2`, `bubbles/v2` — framework, styling,
  the flat list.
- `github.com/taigrr/bubbleterm` — embedded terminal widget (CSI/OSC/ESC/DCS,
  screen state, 256/true color), `NewWithPipes`.
- `github.com/creack/pty` — wraps the `cs ssh` subprocess in a local PTY so SSH
  sees a tty and propagates size to the remote.

Fallback if fidelity is insufficient: `github.com/charmbracelet/x/vt`.

### Size propagation (the crux)

Keep the local PTY winsize, the bubbleterm model size, and the content rectangle
in lockstep:

```go
cmd := exec.Command("cs", "ssh", "-t", sandbox, "--", "claude", "attach", id)
ptmx, _ := pty.Start(cmd)
pty.Setsize(ptmx, &pty.Winsize{Cols: w, Rows: h})
term, _ := bubbleterm.NewWithPipes(int(w), int(h), ptmx, ptmx)
```

On any content-pane resize, call `pty.Setsize` and `term.Resize` together from a
single centralized function.

### Model shape

```go
type focus int
const ( focusSidebar focus = iota; focusContent )

type row struct {                 // one aggregated session
    sandbox, id, name, state string
    pr   string
    age  time.Duration
}

type session struct {             // the one attached connection
    sandbox, id string
    ptmx *os.File
    term *bubbleterm.Model
    cmd  *exec.Cmd
}

type model struct {
    rows  []row                   // flat, from the aggregator
    list  list.Model
    cur   *session                // attached session, nil if none
    focus focus
    sideW, w, h int
}
```

Only the attached session has a live `session`. Rows come from the aggregator;
selecting one starts an attach. The `(sandbox, id)` key is Claude’s identity, so
we keep no session bookkeeping of our own.

### Focus and input routing

A single toggle key flips `focus`. Sidebar focus forwards keys to `list.Update`
(`enter` attaches the highlighted row). Content focus encodes `KeyPressMsg` to
bytes and writes to `cur.ptmx`. Reserve only the toggle key. Focus is plain app
state, so there’s no key-stealing problem.

### Switching, reconnect, resume

Selecting a different row tears down `cur` (killing the SSH client detaches the
session cleanly via the supervisor), then attaches the new one under a fresh PTY.
Watch `cmd.Wait()` in a goroutine; on an unexpected drop, rebuild the PTY and
emulator and reattach (the session is still alive in the supervisor). Selecting a
suspended sandbox blocks through resume; show “resuming…” until Running, then the
emulator comes up on the respawned session. A finished session is marked done.

### View

Manual two-column lipgloss layout: fixed-width flat list joined to the content
region via `JoinHorizontal`. Content renders `cur.term.View()` when attached, a
placeholder (including “resuming…”) otherwise. Draw PR badges and state markers
in the rows ourselves from the aggregated data.

## Liveness (“which session needs me”)

Baseline from the aggregator (`claude agents --json` carries state). Map to row
markers. Optionally add Claude Code `Notification`/`Stop` hooks posting to an
in-process listener goroutine for a low-latency needs-input push that triggers an
immediate re-poll. Hybrid: poll baseline, push edges.

## Open questions / risks to verify first

- **Emulation fidelity (make-or-break for this variant).** Spike bubbleterm
  against a real attached session: alt-screen, rapid redraws, true/256 color,
  wide/CJK runes, bracketed paste, cursor shape, OSC title, mouse passthrough. If
  it corrupts, fall back to `charmbracelet/x/vt` or spec C.
- **Supervisor survives disconnect (central assumption).** Confirm a `claude --bg` session survives SSH disconnect inside a Crafting workspace and reappears
  in `claude agents --json`. If it dies, add a per-sandbox persistence anchor
  rather than abandoning the approach; only an unusable agent view sends you to
  A/B. **Test first.**
- **Suspend/respawn round trip.** Confirm suspend stops sessions and `claude respawn --all` in `on_resume` restores them intact.
- **`claude agents --json` shape.** Newly shipped; confirm fields and
  non-interactive behavior over `cs ssh`.
- **Probe exit-code convention.** Confirm with a one-line probe.
- **Agent view through Vertex AI.** Verify availability in the Compass/Vertex
  environment, not just personal Max-plan use.
- **Polling cost / quota fan-out.** Bound concurrency and cache; surface a
  running session count.

## POC milestones

1. **Fidelity spike**, full-window bubbleterm running `cs ssh -t … claude attach <id>`; judge the fidelity checklist. Decides whether D is viable vs C.
1. **Supervisor survival test**, dispatch `claude --bg` over `cs ssh`,
   disconnect, reconnect, confirm still running; if it died, retry with a
   per-sandbox persistence anchor and confirm. Then suspend/resume/`respawn --all` round trip.
1. Parse `claude agents --json` over `cs ssh`; build the flat aggregated list
   across two sandboxes with concurrent polling and caching.
1. Two-column layout, focus toggle, lazy attach with correct PTY sizing.
1. Custom activity probe (working-state) and `on_resume` respawn hook.
1. Reconnect handling, dispatch input, optional hook-based liveness push.