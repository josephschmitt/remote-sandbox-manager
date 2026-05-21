# NOTES.md — track-c POC build session

What's done, what's stubbed, and what a reviewer should know before judging.

## What runs

Against `MockBackend`, with no Crafting access:

1. `./scripts/launch.sh` starts a tmux server on socket `agentmgr` with the
   custom `agentmgr.conf`, then opens one session "mgr" with one window
   "main" split horizontally: sidebar in pane 0, placeholder in pane 1.
2. The sidebar (Bubble Tea v2, in pane 0) renders six fake sessions
   grouped by sandbox (`sb-alpha`, `sb-bravo`, `sb-charlie`) with per-row
   state markers and ages that drift forward between polls.
3. `j`/`k`/`g`/`G` move the cursor. `Enter` on a row triggers
   `tmux new-window` with the content window running
   `agentmgr attach --mock <sb> <id>`, which streams the MockBackend's
   scripted ANSI (clear, banner, colors, a 24-frame spinner) into the
   content pane. tmux switches focus to the new window.
4. `Enter` on a different row creates a second tmux window; previously
   attached windows stay alive in the background with their state intact
   (the actual DoD #5 requirement).
5. `F12` toggles focus between sidebar pane and the active content pane
   (only key tmux intercepts; everything else reaches the pane).
6. Terminal resize works at every layer: the Bubble Tea sidebar gets a
   `WindowSizeMsg` and reflows the row layout (truncating columns at narrow
   widths, restoring them at wide), the placeholder respects size, and
   tmux's `aggressive-resize on` resizes inactive content windows on
   refocus.

All six DoD points (launch, navigate, attach, focus toggle, switch without
losing state, resize) are demonstrably satisfied; this was smoke-tested by
driving the assembled layout headlessly with `tmux send-keys` /
`capture-pane`. Reproduce by running `./scripts/launch.sh` in a real
terminal.

## What's stubbed

### `CraftingBackend` (backend/crafting.go)

Real backend exists end-to-end but with assumptions that need a live
sandbox to confirm — see VERIFY.md for the exact commands. In summary:

- `csList` and `claudeAgents` JSON shapes are best-guess; field names
  (`age_seconds`, `last_activity`, `pr`) are placeholders likely to
  diverge from the real output. The decode will not crash on unknown
  fields (Go's `encoding/json` is lenient), but missing fields will
  silently zero out — `last_activity` falls through to a 0 age, etc.
- The `state != "running"` sandbox filter assumes `cs list --json` uses
  literal `"running"`. Adjust if Crafting uses different state strings.
- `CraftingBackend.Attach` is a **STUB** that returns an error. Track C
  attaches via tmux respawn-pane and the `attach --real` subcommand
  (which execs `cs ssh -t <sb> -- claude attach <id>` directly), so the
  in-process `Attach` is never called on the hot path. The interface is
  still implemented for parity with track-d. If something later wants
  in-process attach (tests, inline preview, etc.), implement it then.
- `Dispatch` parses the session id naively as "last whitespace token of
  stdout"; the real `claude --bg` output format is unverified.
- No caching of last-known rosters for suspended sandboxes; we just elide
  them. The spec says to show them dimmed; that's a small follow-up.

### Activity probe and on_resume hook

Out of scope for the build session: those live in the Crafting workspace
definition, not the manager binary. VERIFY.md describes the test for the
exit-code convention; the actual probe script and workspace YAML belong
in the Crafting workspace, not here.

### Liveness push (Notification/Stop hooks)

Listed as optional in spec C §"Liveness". Not implemented in the POC.
Baseline state from polling is enough for the comparison.

### Dispatch UI

`Backend.Dispatch` is wired and works in the mock (`Backend.Dispatch`
appends a new fake session that shows up in the next poll). The sidebar
does not yet expose a dispatch input modal — that's a follow-up. The
relevant code paths are in place; one Bubble Tea key binding (`n`?)
opening a textinput is all that's missing.

## Decisions

1. **One tmux window per attached session** (not pane-respawn). The spec
   showed `respawn-pane -k`, which kills the previous content on each
   selection. That would fail DoD #5 ("switch without losing the others'
   state"). Windows give us free per-session state isolation, and tmux's
   `aggressive-resize on` keeps them in sync on focus.

2. **Bubble Tea v2 stable, not alpha**. We pulled `charm.land/bubbletea/v2
   v2.0.6` and `charm.land/lipgloss/v2 v2.0.3` — the released stable line.
   Note the import path moved from `github.com/charmbracelet/bubbletea/v2`
   to `charm.land/bubbletea/v2`; the v2 `Model` interface changed too
   (`Init()` returns just `Cmd`, `View()` returns `tea.View` rather than
   `string`). Track D will hit the same wall; we agreed on the seam
   ahead of time so this divergence shouldn't bite the comparison.

3. **Window-name separator is `~`, not `/` or `.`**. tmux's target
   parser treats `:` and `.` as session/window/pane separators, and `/`
   tripped target lookup in testing (windows named `sb-alpha/j7K2`
   couldn't be re-selected after creation). `~` is unambiguous and
   visually distinct.

4. **Subcommand-shaped binary** (`sidebar`, `attach --mock`, `attach
   --real`, `attach --placeholder`, `roster`). The launcher script
   re-invokes the same binary in different roles via tmux. This keeps the
   surface small (one Go build) and makes the tmux integration tractable
   — tmux just runs `argv[0]` strings.

5. **Mock attach streams in a loop**. After the scripted ANSI ends, the
   mock attach prints "(replaying in 3s)" and re-runs the script. A
   reviewer leaving a content window in focus continues to see motion;
   coming back to it after switching away still shows recognizable
   content (and the `g`/`G`/replay loop means the demo doesn't go fully
   blank). The MockBackend's pipe doesn't close after the script ends
   (its documented contract is to idle), so `attach.Mock` uses a
   quiescence timer (600ms of no bytes => script done) to detect the end
   without changing the MockBackend API.

## Open questions hit during the build

- **Bubble Tea v2 `Init` semantics.** I expected the docs' older shape
  (`Init() (tea.Model, tea.Cmd)`) — the stable v2 release returns just
  `tea.Cmd`. Worth verifying track D landed on the same shape so the
  apples-to-apples isn't accidentally apples-to-pears.
- **tmux `aggressive-resize on` vs alt-screen Bubble Tea programs.**
  Bubble Tea v2 sets alt-screen via `tea.View{AltScreen: true}` per
  render. With `aggressive-resize on`, an inactive sidebar window's
  Bubble Tea program may stop receiving WindowSizeMsg until refocus. In
  our layout the sidebar is *always* visible in pane 0 of window "main"
  so this hasn't surfaced; mentioning here for follow-up.
- **`agentmgr.conf` minimalism.** The config is intentionally short —
  status off, mouse on, F12 binding, kill-server on `q`. No theming, no
  status line. Spec C is explicit that we don't reuse the user's tmux
  config. If a reviewer wants nicer chrome, that's a follow-up.

## What a reviewer should look at first

1. `internal/sidebar/sidebar.go` — the actual Bubble Tea program. The
   `Update` and `View` methods are the comparable surface against track-d.
2. `internal/tmuxctl/tmuxctl.go` — every tmux command the sidebar fires.
   This is the bulk of the "tmux is plumbing" claim. Note it's ~80 LOC.
3. `scripts/launch.sh` + `scripts/agentmgr.conf` — the whole tmux setup.
   17 lines of config and ~40 of bash. Compare against track-d's
   equivalent (which has no chrome surface — bubbleterm is doing it).
4. `backend/crafting.go` — the live backend shape. Compare against
   track-d's; should be near-identical except for whether Attach is
   stubbed (it is, for track C, on purpose).
5. The DoD smoke test in VERIFY.md item 5 (switch+resize) — that's the
   thing this POC most directly demonstrates.
