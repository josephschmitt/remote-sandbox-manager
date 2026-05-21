# agentmgr — the bubbleterm variant

Cross-sandbox Claude agent manager. Single Go binary that aggregates
Claude Code background sessions across every Crafting sandbox into one
flat list, and lets you attach to any of them via an embedded terminal
emulator. The native variant: Bubble Tea v2 owns all chrome, no tmux,
one process.

See `../spec-d-agentview-bubbleterm.md` and `../build-orchestrator.md`
for the design context.

## Layout

```
track-d/
  main.go                       # entry point, picks backend
  backend/
    backend.go                  # the Backend interface (shared seam)
    mock.go                     # MockBackend — fake roster + ANSI stream
    crafting/crafting.go        # CraftingBackend — shells out to cs/claude
  ui/
    model.go                    # Bubble Tea root model
    styles.go                   # lipgloss styles
    model_test.go               # unit tests (no TTY required)
  cmd/
    spike/                      # interactive fidelity spike
    spike-headless/             # headless ANSI-stream fidelity check
  README.md  VERIFY.md  NOTES.md
```

## Toolchain

Go is pinned via [devbox](https://www.jetify.com/devbox). `devbox.json`
declares `go@1.26`, which matches the version in `go.mod`. Everything
below assumes you either prefix commands with `devbox run --` or open a
`devbox shell` first; a system Go on the right minor will also work.

## Running against the mock (the demo path)

```sh
devbox run -- go run .
# or, inside `devbox shell`:
go run .
```

You should see a sidebar with six fake sessions across three pretend
sandboxes (`payments-api`, `analytics-ingest`, `observability`), with
per-row state markers. Keys:

  - `↑`/`↓` or `k`/`j` — move the selection
  - `enter` — attach the highlighted session (lazy; you'll see a
    scripted ANSI stream draw into the content pane)
  - `tab` — toggle focus between the sidebar and the attached terminal
  - `q` — quit (when sidebar focused)
  - `ctrl+c` / `ctrl+\` — quit unconditionally

Switching to a different row and back preserves each emulator's state,
which is one of the Definition-of-Done requirements.

## Running against the real Crafting backend

```sh
devbox run -- go run . --real
# or
AGENTMGR_BACKEND=crafting devbox run -- go run .
```

This expects `cs` and `claude` on PATH. The backend is partly stubbed
overnight — see `NOTES.md` for the full list of unverified JSON shapes
and exit-code conventions, and `VERIFY.md` for the exact commands to
run before trusting it. The mock is the recommended demo path.

## Tests

```sh
devbox run -- go test ./...
```

Tests cover roster aggregation, navigation, attach, focus toggle,
switch-and-reuse, resize, render, and poll refresh. They run without a
TTY so they're safe in CI.

## Fidelity spikes

Interactive (needs a TTY):

```sh
devbox run -- go run ./cmd/spike            # runs bash through bubbleterm
devbox run -- go run ./cmd/spike vim        # alt-screen + cursor + scroll
```

Headless (no TTY needed):

```sh
devbox run -- go run ./cmd/spike-headless
```

The headless run feeds a representative ANSI stream (alt-screen, SGR
256/true-color, ED, CUP, DECSC/DECRC, wide CJK runes) and prints the
resulting frame so you can judge fidelity in a CI box. The current
result is in `NOTES.md`: bubbleterm passes; no fallback needed.
