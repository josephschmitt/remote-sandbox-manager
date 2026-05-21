# agentmgr (track C) — tmux-layout cross-sandbox agent manager

A POC of the spec-C variant: tmux as an invisible layout engine, a Bubble Tea
v2 sidebar in the left pane, and the attached Claude Code session running in
the right pane(s). The cross-sandbox aggregator lives in the sidebar; tmux
owns the chrome (focus, switching, resize propagation).

This track is one of two parallel implementations being compared. Track D
(native bubbleterm) lives in `../track-d/`. The two share an identical
`backend.Backend` seam (`backend/backend.go`) so a renderer could later be
swapped onto either backend.

## Toolchain (devbox)

The Go toolchain is pinned via [devbox](https://www.jetify.com/devbox) so
the build is reproducible. `devbox.json` declares `go@1.25` (matching the
`go` directive in `go.mod`); everything below assumes you either prefix
commands with `devbox run --` or drop into a `devbox shell` first.

```sh
cd track-c
devbox shell               # one-time: drops you into a shell with go@1.25
# ...or prefix individual commands:
devbox run -- go version
```

## Demo path (mock, no Crafting access needed)

The mock backend ships fake sessions across three pretend sandboxes and a
scripted ANSI stream that runs in the content window so you have something
real to watch.

```sh
cd track-c
devbox run -- go build -o agentmgr .
devbox run -- ./scripts/launch.sh   # opens tmux on its own -L agentmgr socket
```

Inside the tmux layout:

| key             | what it does                                                      |
| --------------- | ----------------------------------------------------------------- |
| `j` / `k`       | move the cursor in the sidebar                                    |
| `g` / `G`       | jump to first / last row                                          |
| `Enter`         | attach to the highlighted session (new tmux window or refocus)    |
| `x`             | close the content window for the highlighted session              |
| `r`             | manual roster refresh                                             |
| `F12`           | toggle focus between sidebar pane and content pane                |
| `q` (in sidebar)| quit the sidebar process                                          |
| `C-b q`         | kill the whole agentmgr tmux server                               |

Mock sessions are pre-populated so you can immediately:

- watch the per-row state markers (`●` working, `!` needs-input, `○` idle,
  `✓` completed, `✗` failed, `■` stopped);
- hit `Enter` on a row to attach (a new tmux window appears, the mock
  scripted stream paints into the right pane);
- hit `Enter` on a different row to attach a second session (a second
  tmux window appears, alongside the first);
- switch between them via tmux (`C-b n`, `C-b p`, `C-b 1/2/...`, or
  `F12` after focusing the sidebar with `C-b 0`);
- resize the terminal — the sidebar reflows and the content windows
  receive SIGWINCH on next focus (`aggressive-resize on`).

The launcher script (`scripts/launch.sh`) is idempotent. Re-running it
attaches to the existing agentmgr server if one is up.

## Running against real Crafting

```sh
devbox run -- ./scripts/launch.sh --real   # uses backend.CraftingBackend
```

This shells out to `cs` and `claude`:

- `cs list --json` for the sandbox roster (assumed shape — see `VERIFY.md`)
- `cs ssh <sandbox> -- claude agents --json` per running sandbox, concurrent,
  bounded at 8 workers
- on `Enter`, the content window runs `cs ssh -t <sandbox> -- claude attach <id>` via the spec-C reconnect wrapper (auto-reconnect on SSH drop)

Parts of the live backend are stubbed pending verification of the actual
JSON shapes — see `NOTES.md` for the list and `VERIFY.md` for the checklist.

## Layout

```
┌─ tmux server on socket "agentmgr", config scripts/agentmgr.conf ─────┐
│ window "main"                                                        │
│ ┌──── pane 0 ────┬─ pane 1 ─────────────────────────────────────────┐│
│ │ sidebar        │ placeholder until selection                      ││
│ │ (Bubble Tea v2)│                                                  ││
│ │                │                                                  ││
│ │ — flat list of │                                                  ││
│ │   all sessions │                                                  ││
│ │   across mock  │                                                  ││
│ │   sandboxes    │                                                  ││
│ └────────────────┴──────────────────────────────────────────────────┘│
│ window "sb-alpha~j7K2"  (created on first Enter)                     │
│   single pane running `agentmgr attach --mock sb-alpha j7K2`         │
│ window "sb-bravo~k8M1"  (created on Enter for that row)              │
│   single pane running `agentmgr attach --mock sb-bravo k8M1`         │
│ ...                                                                  │
└──────────────────────────────────────────────────────────────────────┘
```

Each attached session gets its own tmux window. That's what makes the DoD's
"switch between sessions without losing state" requirement free: the OS owns
each pane's tty state and tmux re-renders it on focus. Switching back to a
previously-attached session lands on the same buffer with the same scroll
position; closing one window (via the sidebar's `x` or `C-b &`) does not
affect the others.

## Repository layout

```
track-c/
├── README.md, VERIFY.md, NOTES.md
├── go.mod, go.sum
├── main.go                          subcommand dispatch
├── backend/
│   ├── backend.go                   shared seam (do not edit)
│   ├── mock.go                      shared MockBackend (do not edit API)
│   └── crafting.go                  live CraftingBackend (some STUBs)
├── internal/
│   ├── sidebar/sidebar.go           Bubble Tea v2 sidebar program
│   ├── attach/attach.go             content-pane child process
│   └── tmuxctl/tmuxctl.go           thin wrapper over `tmux -L agentmgr`
└── scripts/
    ├── agentmgr.conf                tmux config for the agentmgr socket
    └── launch.sh                    entry point (mock or --real)
```

## Subcommands (the binary, directly)

You normally never invoke these yourself — `scripts/launch.sh` and the
sidebar do. Listed for reference / debugging:

```sh
agentmgr sidebar [--real]              # the Bubble Tea sidebar
agentmgr attach --placeholder          # the idle right-pane screen
agentmgr attach --mock <sb> <id>       # replay the mock attach stream
agentmgr attach --real <sb> <id>       # cs ssh -t <sb> -- claude attach <id>
agentmgr roster [--real]               # one-shot roster dump (no tmux, no TUI)
```

The `roster` subcommand is the fastest sanity check that the backend wiring
works:

```sh
$ ./agentmgr roster
6 sessions:
  sb-alpha      j7K2    refactor-auth           working       draft #142
  sb-alpha      p3Q9    fix-flaky-test          needs-input
  sb-alpha      x1B4    audit-deps              idle
  sb-bravo      k8M1    ingest-pipeline         working       open #87
  sb-bravo      z5N7    doc-pass                completed     merged #84
  sb-charlie    v2R6    spike-tracing           failed
```

## Build & verify

```sh
devbox run -- go build ./...      # everything compiles
devbox run -- go vet ./...        # vet clean
devbox run -- ./agentmgr roster   # mock backend wires up; six sessions appear
devbox run -- ./scripts/launch.sh # full tmux demo (run in a real terminal)
```

Inside a `devbox shell` you can drop the `devbox run --` prefix.
