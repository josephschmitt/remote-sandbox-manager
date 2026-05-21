# Remote Sandbox Manager

A local manager that aggregates Claude Code background sessions across many
[Crafting](https://crafting.dev) sandboxes into one flat list, and lets you
attach to any of them.

## Why

Claude Code ships an agent view (`claude agents`): a per-machine supervisor
that hosts multiple background sessions and exposes `claude agents --json`,
`claude attach <id>`, `claude --bg`, and `claude respawn --all`. That removes
the need to build an in-sandbox multiplexer. What it does **not** provide is a
view across machines — and each Crafting sandbox is a separate machine with
its own supervisor. This tool fills that gap.

In a sandbox, Claude Code owns session lifecycle and identity. Across
sandboxes, this manager polls each one's roster, flattens every session into
one list keyed by `(sandbox, session-id)`, and attaches on selection. No
dtach, no per-agent sockets, no session-id bookkeeping of our own.

## What it looks like

A best-effort render of the manager attached to a session, with the
`MockBackend` roster across three sandboxes:

```
╭──────────────────────────────┬───────────────────────────────────────────────╮
│                              │                                               │
│   payments-api               │ payments-api/refactor-auth · ~/repo           │
│                              │                                               │
│ ▶ ● refactor-auth       42s  │ ● refactoring auth middleware                 │
│      draft #142              │                                               │
│   ◆ fix-flaky-test       5m  │ > Read src/auth/middleware.go                 │
│   ○ audit-deps          12m  │   └  read 238 lines                           │
│                              │                                               │
│   analytics-ingest           │ > Edit src/auth/middleware.go                 │
│                              │   └  applied 3 edits                          │
│   ● ingest-pipeline     18s  │                                               │
│      open #87                │ > Bash go test ./auth/...                     │
│   ✓ doc-pass            47m  │   └  PASS: 12 tests in 0.42s                  │
│                              │                                               │
│   observability              │                                               │
│                              │ ╭───────────────────────────────────────────╮ │
│   ✗ spike-tracing       31m  │ │ > _                                       │ │
│                              │ ╰───────────────────────────────────────────╯ │
╰──────────────────────────────┴───────────────────────────────────────────────╯
```

The left sidebar groups every Claude background session by sandbox, with
state markers (● working, ◆ needs input, ○ idle, ✓ completed, ✗ failed) and
optional PR badges. The selected row (▶) drives the right pane — that pane
is where the two variants differ: the **tmux** variant respawns a tmux pane
to `cs ssh -t <sb> -- claude attach <id>`; the **bubbleterm** variant drives
the same command through a local PTY into an embedded `bubbleterm` widget.

## Status: two implementations under comparison

Both share an identical `Backend` interface and `MockBackend`, and a Bubble
Tea v2 sidebar. They differ in exactly one layer: how the selected session is
rendered locally.

| Variant | Renderer | Branch | Code |
|---|---|---|---|
| **tmux** | tmux as an invisible layout engine | [`track-c-tmux`](https://github.com/josephschmitt/remote-sandbox-manager/tree/track-c-tmux/track-c) | `track-c/` |
| **bubbleterm** | single Go binary with an embedded terminal emulator ([`taigrr/bubbleterm`](https://github.com/taigrr/bubbleterm)) | [`track-d-bubbleterm`](https://github.com/josephschmitt/remote-sandbox-manager/tree/track-d-bubbleterm/track-d) | `track-d/` |

Both build, both run against `MockBackend` with no Crafting access. See each
branch's `track-*/README.md` for how to demo.

## Specs

Full design lives in [`specs/`](specs/):

- [`build-orchestrator.md`](specs/build-orchestrator.md) — coordination
  layer: shared decisions, the shared `Backend` interface, Definition of Done,
  comparison criteria.
- [`spec-c-agentview-tmux.md`](specs/spec-c-agentview-tmux.md) — the tmux
  variant's design.
- [`spec-d-agentview-bubbleterm.md`](specs/spec-d-agentview-bubbleterm.md) —
  the bubbleterm variant's design.

## Layout

```
.
├── README.md                  (this file)
├── specs/                     full design docs
└── track-{c,d}/               on the respective branches
    ├── backend/               shared seam: Backend interface + MockBackend
    ├── …                      variant-specific renderer
    ├── README.md              how to run that track
    ├── VERIFY.md              live-infra checklist
    └── NOTES.md               what's stubbed, decisions made
```

The shared seed commit on `main` contains the `Backend` interface and
`MockBackend` so both variants start from the same byte-identical seam —
that's what makes the comparison apples-to-apples.
