# AGENTS.md

Orientation, rules, and pointers for any agent picking this repo up cold.
Read `README.md` for the user-facing pitch; this file is the working contract.

## What this is

A local manager that aggregates Claude Code background sessions across
remote Crafting sandboxes into one flat sidebar. Two competing
implementations are under comparison, on parallel branches:

| Variant | Renderer | Branch | Code |
|---|---|---|---|
| **tmux variant** | tmux as an invisible layout engine | `track-c-tmux` | `track-c/` |
| **bubbleterm variant** | single Go binary with embedded terminal emulator ([`taigrr/bubbleterm`](https://github.com/taigrr/bubbleterm)) | `track-d-bubbleterm` | `track-d/` |

`main` carries the specs, the root README, and a seed Go scaffold
(`backend.Backend` interface + `MockBackend`) in both `track-c/` and
`track-d/`. The full variant code lives on the two feature branches; each
extends the same seed.

## Hard rules

1. **Don't change `backend.Backend` or `MockBackend`'s exported API.** Both
   files are byte-identical across `track-c/` and `track-d/` by design —
   that's what keeps the comparison apples-to-apples. Shared changes go on
   `main` and propagate to both branches via rebase.

2. **Don't reintroduce "Track C / Track D" labels in prose.** Earlier
   sketches A and B never shipped; the surviving letters are vestigial and
   confuse new readers. Refer to the variants as the **tmux variant** and
   the **bubbleterm variant**. (Branch names `track-c-tmux` /
   `track-d-bubbleterm` and directory names `track-c/` / `track-d/` stay
   as-is — they're functional identifiers; the prose is what matters.) The
   README's "Status" section explains the lineage for users.

3. **Use the established mock sandbox names.** The `MockBackend` ships six
   sessions across three pretend sandboxes: `payments-api`,
   `analytics-ingest`, `observability`. The README diagram, both variants'
   READMEs/NOTES, and `track-d/ui/model_test.go` all assert on these — if
   you change them, you have a sweep to do (see `docs/workflow.md`).

4. **Go toolchain via [devbox](https://www.jetify.com/devbox).** Both
   variants pin `go@1.26` in `track-{c,d}/devbox.json` and `go 1.26.0` in
   `go.mod`. Run commands as `devbox run -- go ...`, or `devbox shell` first.

5. **`MockBackend` is the demo default.** Both binaries must run against
   the mock with zero external dependencies. The Crafting backend (which
   shells out to `cs` and `claude`) is gated behind `--real`.

## Repo layout

```
.
├── README.md                  user-facing pitch
├── AGENTS.md                  (this file)
├── CLAUDE.md                  → AGENTS.md  (symlink)
├── docs/                      supplementary agent context
├── specs/                     full design docs
├── track-c/                   shared seed on main; full variant on track-c-tmux
└── track-d/                   shared seed on main; full variant on track-d-bubbleterm
```

`main` is intentionally lean — it only carries the apples-to-apples seed.
Variant code is on the feature branches.

## Workflow at a glance

- **Variant-only changes** go on the corresponding feature branch
  (`track-c-tmux` or `track-d-bubbleterm`).
- **Shared changes** (`backend/backend.go`, `backend/mock.go`, specs, root
  README, this file, docs) go on `main`, then both feature branches rebase
  onto the new tip. See **`docs/workflow.md`** for the recipe — mechanical
  but easy to forget a step.
- Feature branches are rebased often; force-push with
  `--force-with-lease`. Don't force-push `main`.

## Pending work

- **`specs/local-agent-support.md`** — host-runner abstraction so localhost
  is just another host alongside Crafting sandboxes. Handoff doc; not
  started.

## Where to look next

| If you want… | Read… |
|---|---|
| user-facing pitch, screenshots, comparison table | `README.md` |
| coordination layer, shared `Backend` seam, DoD, comparison criteria | `specs/build-orchestrator.md` |
| each variant's design | `specs/spec-c-agentview-tmux.md`, `specs/spec-d-agentview-bubbleterm.md` |
| what each variant actually shipped, what's stubbed | `track-{c,d}/NOTES.md` (on the feature branch) |
| real-infra verification checklist | `track-{c,d}/VERIFY.md` (on the feature branch) |
| how to make a shared change without breaking a branch | `docs/workflow.md` |
