# Build Orchestrator: Cross-Sandbox Agent Manager (parallel C/D build)

## Objective

Build two competing implementations of the same tool, in parallel and in
isolation, so they can be compared side by side. Both are a local manager that
aggregates Claude Code background sessions across many Crafting sandboxes into
one flat list and lets you attach to any of them. They differ in exactly one
layer: how the selected session is rendered locally.

- **Track C** — tmux as an invisible layout engine. Spec: `spec-c-agentview-tmux.md`.
- **Track D** — single Go binary embedding a terminal emulator (bubbleterm).
  Spec: `spec-d-agentview-bubbleterm.md`.

Read each track’s spec in full; this doc is the coordination layer, not a
replacement for them.

## Context

Claude Code shipped agent view (`claude agents`): a per-machine supervisor that
hosts multiple background sessions and exposes `claude agents --json`,
`claude attach <id>`, `claude --bg`, and `claude respawn --all`. That removes the
need to build an in-sandbox multiplexer. What it does not provide is a view
across machines, and each Crafting sandbox is a separate machine with its own
supervisor. This tool fills that gap: a flat aggregator across sandboxes. Specs A
and B (self-built dtach supervision) exist as a fallback and are out of scope for
this build.

## Shared decisions (apply to both tracks)

- **Flat aggregator over agent view, not self-supervised**: Claude Code’s
  supervisor already manages per-sandbox sessions; we only aggregate across
  sandboxes.
- **No dtach in the happy path**: the supervisor is assumed to keep sessions
  alive across disconnect. A persistence anchor is added only if the survival
  test fails (see Verification).
- **Go + Bubble Tea v2 for the sidebar in both tracks**: keeps the only real
  difference the rendering layer, so the comparison is apples-to-apples.
- **Roster via `claude agents --json`, attach via `claude attach <id>`, dispatch
  via `claude --bg`**: shipped primitives, no bookkeeping of our own.
- **Suspend via a working-state activity probe; resume via `claude respawn --all`
  in `on_resume`**: cost tracks real work; resume is seamless.
- **All Crafting/`cs` interaction sits behind one `Backend` interface**: so the
  unverified parts are swappable and the overnight build doesn’t block on live
  infra.

## Shared backend seam (build this first, identically in both tracks)

Both tracks implement the same interface and ship two backends: a real one that
shells out to `cs`/`claude`, and a mock that fakes sessions and a scripted
terminal stream so the UI runs and demos without any Crafting access.

```go
type Session struct {
    Sandbox string        // cs sandbox id
    ID      string        // claude session id (dir under ~/.claude/jobs/)
    Name    string
    State   string        // working | needs-input | idle | completed | failed | stopped
    PR      string        // optional PR url/status
    Age     time.Duration
}

type Backend interface {
    // List = cs list (JSON) joined with per-sandbox `claude agents --json`,
    // flattened. Concurrent, cached, suspended sandboxes shown dimmed.
    List(ctx context.Context) ([]Session, error)
    // Attach = cs ssh -t <sandbox> -- claude attach <id>, returned as a PTY-backed
    // stream the renderer drives. (Track C respawns a tmux pane to this command
    // instead of consuming the stream directly.)
    Attach(ctx context.Context, sandbox, id string) (io.ReadWriteCloser, error)
    Dispatch(ctx context.Context, sandbox, name, prompt string) (id string, err error)
    Stop(ctx context.Context, sandbox, id string) error
    Respawn(ctx context.Context, sandbox string) error // claude respawn --all
}
```

`MockBackend` returns a fixed set of fake sessions across two or three fake
sandboxes in varied states, and on `Attach` streams a short scripted ANSI
sequence (some color, a redraw, a cursor move) so the renderer has something real
to show. `CraftingBackend` is the live implementation; it can stay partly stubbed
overnight, with the gaps listed in NOTES.md.

Keeping this interface byte-for-byte identical across both tracks is what makes
the comparison fair and what would let a winning renderer drop onto either
backend later.

## Verification reality (do not block on it)

The load-bearing assumptions require a live sandbox and `cs`, which the build
session may not have. So: build against the documented assumptions behind the
`Backend` interface, ship the mock so everything is runnable, and emit a
`VERIFY.md` with the exact commands to run against real infra in the morning.
Each track’s `VERIFY.md` covers, at minimum:

1. **Supervisor survival**: `cs ssh <sb> -- claude --bg "…"`, disconnect,
   reconnect, `claude agents --json` — still running? If not, the fix is one
   persistence anchor per sandbox (workspace daemon or a dtach-held supervisor),
   not a per-agent muxer.
1. **Suspend/resume**: suspend, resume, `claude respawn --all`, conversation
   intact?
1. **`claude agents --json` shape**: confirm field names (shipped recently);
   adjust the `Session` decode to match.
1. **Probe exit-code convention**: confirm which exit code Crafting reads as
   active before wiring the probe.

## Parallel execution

Dispatch two independent build sessions, each scoped to one track, each in its
own isolated worktree so they can’t edit each other. The natural way, given this
is what the tool itself is about, is agent view:

```
claude --bg --name "agentmgr-track-c" "<Track C prompt below>"
claude --bg --name "agentmgr-track-d" "<Track D prompt below>"
```

Each background session isolates into its own worktree under `.claude/worktrees/`
automatically. Alternatively, two plain checkouts (`agentmgr-c`, `agentmgr-d`).
Place all four specs plus this orchestrator in the project root first so each
session can read its spec.

**Track C dispatch prompt:**

> Build the cross-sandbox Claude agent manager described in
> `spec-c-agentview-tmux.md` and `build-orchestrator.md` in this repo. This is the
> tmux-layout track. Implement the shared `Backend` interface from the
> orchestrator exactly as written, ship both `MockBackend` and a (possibly partly
> stubbed) `CraftingBackend`, and make the whole thing run against `MockBackend`
> with no Crafting access. Satisfy the shared Definition of Done. Write README.md,
> VERIFY.md, and NOTES.md. Do not gold-plate; this is a POC for comparison.

**Track D dispatch prompt:**

> Build the cross-sandbox Claude agent manager described in
> `spec-d-agentview-bubbleterm.md` and `build-orchestrator.md` in this repo. This
> is the native bubbleterm track. Do the fidelity spike first (see spec milestone
> 
> 1. — if bubbleterm can’t render a real Claude TUI cleanly, note it loudly and
>    fall back to `charmbracelet/x/vt`. Implement the shared `Backend` interface from
>    the orchestrator exactly as written, ship both `MockBackend` and a (possibly
>    partly stubbed) `CraftingBackend`, and make it run against `MockBackend` with no
>    Crafting access. Satisfy the shared Definition of Done. Write README.md,
>    VERIFY.md, and NOTES.md. Do not gold-plate.

## Definition of done (identical for both tracks)

Running against `MockBackend`, each implementation must:

1. Launch and render a flat sidebar of fake sessions across at least two fake
   sandboxes, with per-row state markers.
1. Navigate the list (up/down/select).
1. Attach to a selected session and render its (mock) terminal stream in the
   content pane.
1. Toggle focus between sidebar and content pane.
1. Switch between sessions without losing the others’ state.
1. Handle a terminal resize correctly.
1. Ship `README.md` (how to run, including against the mock), `VERIFY.md` (the
   real-infra checklist), and `NOTES.md` (what’s stubbed, open questions hit,
   decisions made).

## Comparison criteria (what gets evaluated in the morning)

- Rendering quality and fidelity, especially Track D (note: the mock only
  approximates a real Claude TUI; flag what still needs a live attach to judge).
- How much of the frame each track actually controls, and how the chrome feels.
- Total complexity and lines of code for equivalent behavior.
- How attach, switch, and resize feel in practice.
- How clean the `cs`/`Backend` seam ended up, since that’s the swappable part.

## Open questions (carry into VERIFY.md, keep in mind while building)

- Does the supervisor survive SSH disconnect inside a Crafting workspace?
- Does suspend + `respawn --all` restore conversations intact?
- Exact `claude agents --json` field names.
- Probe exit-code convention.
- Agent view availability under Vertex AI (relevant to the Compass environment).
- Polling cost across many sandboxes; quota fan-out across many parallel sessions.

## Starting actions (for whoever coordinates the dispatch)

1. `ls *.md` and `cat spec-c-agentview-tmux.md spec-d-agentview-bubbleterm.md` to
   load both specs.
1. Put all four specs and this orchestrator in the project root.
1. Scaffold each track as a Go module; implement the shared `Backend` interface
   and `MockBackend` before any UI, so both tracks start from the same seam.
1. Dispatch the two build sessions with the prompts above, each in its own
   worktree.

-----

*Generated from a mobile design session. Drop the specs into the project root and
paste the two prompts to kick off both builds in parallel.*