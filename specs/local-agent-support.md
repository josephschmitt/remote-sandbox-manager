# Handoff: Local agent support (host-runner abstraction)

## Objective
Let the remote sandbox manager show Claude Code agents running on the local machine in the same flat sidebar as sandbox agents, by generalizing command transport from "ssh into a Crafting sandbox" to a pluggable runner, so localhost is just another host.

## Context
The remote sandbox manager (specs C and D) aggregates Claude Code background sessions across Crafting sandboxes into one flat sidebar and attaches to any of them. Every session access already goes through Claude Code's own primitives (`claude agents --json`, `claude attach <id>`, `claude --bg`); the only sandbox-specific part is prefixing those with `cs ssh <sandbox> --`. A local agent is reached by running the same commands with no prefix, so local support is the simplest case of the existing design rather than a new code path.

## Decisions already made
- **Introduce a `Runner` under the existing `Backend` seam**: transport (local exec vs `cs ssh <sandbox> --`) is the only real difference, so model it there and let one `Backend` serve both.
- **Host providers, not a hardcoded sandbox list**: localhost is a trivial provider returning one host; `cs list` returns N. The aggregator iterates all providers and merges, opening the door to SSH boxes or containers later as more runners.
- **Key sessions by `(host, session-id)`**, with `"local"` as a host value: the sidebar is already host-agnostic.
- **Lifecycle machinery is a Crafting-runner property, not local**: probe, `on_resume` respawn, and suspend-when-idle are Crafting-specific; the laptop's sleep/wake is handled by Claude Code's own supervisor + `respawn --all`, so the local runner carries none of it.
- **Visual marker + group local sessions by `--cwd`**: a local agent touches real repos while a sandbox agent is sealed, and they otherwise look identical, so local sessions get a distinct marker and project grouping to prevent cueing a destructive action at a local agent thinking it's boxed.

## Approaches considered and rejected
- **A separate parallel `LocalBackend`**: rejected because it would duplicate the aggregator, attach, and liveness paths when the only real difference is a command prefix. A `Runner` under the single `Backend` is DRYer and keeps tracks C and D comparable.

## Constraints
- **Tech stack / language:** Go, Bubble Tea v2. Builds on the shared `Backend` interface defined in `build-orchestrator.md`.
- **Codebase:** the track C and/or D implementations; the change lives in the `cs`-command construction inside `Backend.List` and `Backend.Attach`.
- **Sequencing:** cheapest if applied during the initial C/D build, before `cs ssh` is hard-wired into the attach path. Also works as a retrofit afterward.
- **Style:** clear, self-documenting Go; minimal comments.

## Open questions
- Confirm `claude agents --json` and `claude attach <id>` behave identically run directly vs over `cs ssh` (same binary; verify).
- Confirm Track D's local PTY attach needs no special-casing vs the ssh path (strictly simpler, no ssh between).
- Local scoping: supervisor is per-user-per-machine; use `claude agents --cwd <path>` to scope/group by project. Confirm a single local supervisor is the model.
- Decide the safety model for destructive actions cued at a local agent.

## Starting actions
1. `cat build-orchestrator.md` — the `Backend` interface and `Session` struct are the seam being extended.
2. `rg -n 'cs ssh'` and `rg -n 'claude (agents|attach|--bg)'` across the track(s) to find where transport is hard-wired.
3. Read `List` and `Attach` in `CraftingBackend`; the `cs ssh <sandbox> --` prefix lives there.

## First task
Introduce the runner seam and prove it with one local agent in the sidebar:

```go
type Runner interface {
    // Command builds an exec for `claude <args>` on this host.
    Command(ctx context.Context, args ...string) *exec.Cmd
}
// LocalRunner:    exec.CommandContext(ctx, "claude", args...)
// CraftingRunner: exec.CommandContext(ctx, "cs",
//                   append([]string{"ssh","-t",sandbox,"--","claude"}, args...)...)
```

Refactor `CraftingBackend`'s command construction to go through a `Runner`, add `LocalRunner` and `CraftingRunner{sandbox}`, have `List` fan out across runners and `Attach` pick the runner for the selected session's host, and surface the local host's `claude agents --json` in the sidebar with a `local` marker alongside one sandbox (mock is fine). Defer host-provider generalization, `--cwd` grouping, and the destructive-action safety model to follow-ups.
