# VERIFY — bubbleterm variant real-infra checklist

What this build cannot test from a background agent session. Run these
against a real Crafting workspace with `cs` and `claude` on PATH
before trusting any of it. Each item lists the exact command and the
shape of "passes".

## 0. Fidelity spike (the make-or-break for variant D)

The bubbleterm emulator must render a real Claude TUI cleanly. From a
real terminal:

```sh
go run ./cmd/spike claude attach <some-id>     # or just `claude` for the picker
```

Pass criteria, drawn from spec D's open-questions list:

  - [ ] alt-screen enter/exit works (no scrollback contamination)
  - [ ] rapid redraws don't flicker or duplicate
  - [ ] 256-color and true-color SGR render with correct hue
  - [ ] wide / CJK runes occupy correct column count, no overlap
  - [ ] bracketed paste round-trips
  - [ ] cursor shape (bar/block) is honored
  - [ ] OSC title is set on the host terminal
  - [ ] mouse passthrough reaches Claude (hover/select)

If any of these fail badly, swap the import in `ui/model.go` from
`github.com/taigrr/bubbleterm` to `github.com/charmbracelet/x/vt` and
re-test. The seam is small enough that the swap is one file. **The
deterministic snapshot from `go run ./cmd/spike-headless` already
passes for alt-screen, SGR, ED, CUP, DECSC/DECRC, and wide CJK runes
(see NOTES.md), so the remaining unknowns are all interactive.**

## 1. Supervisor survives SSH disconnect

```sh
cs ssh <sb> -- claude --bg --name verify-1 "echo hi; sleep 600"
# (back on local)
cs ssh <sb> -- claude agents --json | jq '.sessions[] | select(.name=="verify-1")'
# disconnect; wait 30s; reconnect
cs ssh <sb> -- claude agents --json | jq '.sessions[] | select(.name=="verify-1")'
```

Pass: the session is still present and `state` is not `failed`.

If it died on disconnect, the fix is one per-sandbox persistence
anchor (workspace daemon or a dtach-held supervisor) — not a return
to specs A/B. The Backend interface doesn't change either way.

## 2. Suspend / resume / respawn round trip

```sh
cs ssh <sb> -- claude --bg --name verify-2 "while :; do echo .; sleep 1; done"
cs sandbox suspend <sb>
# wait for fully suspended
cs sandbox resume <sb>
# the on_resume hook should run `claude respawn --all`; verify:
cs ssh <sb> -- claude agents --json | jq '.sessions[] | select(.name=="verify-2")'
cs ssh -t <sb> -- claude attach <id>   # should land on the respawned session
```

Pass: session reappears with state `working` (or `idle`), conversation
history is intact, attaching shows the resumed loop.

## 3. `claude agents --json` shape

```sh
cs ssh <sb> -- claude agents --json | jq .
```

Pass: response decodes against `claudeAgentsResponse` in
`backend/crafting/crafting.go` (or a bare array). Adjust the struct if
fields differ from `{id, name, state, pr, age_seconds}` — the decode
is lenient but won't invent missing fields.

Specifically confirm:

  - [ ] `state` values match the constants in `backend/backend.go`
        (working | needs-input | idle | completed | failed | stopped)
  - [ ] there is a session id field (probably `id`)
  - [ ] `pr` is a string, not a struct, or update the decode
  - [ ] there is some kind of age/started field (probably
        `age_seconds`); rename if it's `started_at` etc.

## 4. `cs list --json` shape

```sh
cs list --json | jq .
```

Pass: response decodes as either `[csListEntry, ...]` or
`{sandboxes: [...]}` or `{items: [...]}`. The decode currently tries
all three. Confirm the `id` field name and adjust `csListEntry` if
needed.

## 5. Probe exit-code convention

```sh
cs ssh <sb> -- bash -lc '
  json=$(claude agents --json 2>/dev/null) || exit 1
  echo "$json" | jq -e ".sessions[]? | select(.state==\"working\") | length > 0" >/dev/null 2>&1 && exit 0
  exit 1
'
echo "exit code: $?"
```

Pass: exit code reads as "active" by the Crafting probe convention.
Confirm whether 0 means "keep awake" or "go to sleep".

## 6. Agent view through Vertex AI / Compass

`claude agents --json` is recent and we have only tested it under
Anthropic-direct. Confirm it works under Vertex (the Compass
environment), since that's the load-bearing assumption for the whole
aggregator. If it doesn't work under Vertex, only then fall back to
specs A/B's self-built per-agent dtach.

## 7. Live attach end-to-end through this binary

```sh
go run . --real
```

Pass:

  - [ ] roster appears within the 2s poll interval
  - [ ] enter on a row attaches and the embedded emulator renders the
        live Claude TUI without corruption
  - [ ] tab toggles focus and keystrokes reach the remote claude
  - [ ] resizing the host terminal resizes the remote tty (verify
        with `cs ssh <sb> -- bash -c 'stty size'` from inside)
  - [ ] switching to another row and back preserves the first
        session's screen state

## 8. Polling cost / quota fan-out

If you have a workspace with many sandboxes:

  - [ ] List() completes inside the 3s timeout for >10 sandboxes
  - [ ] Concurrent fan-out is bounded by `poolSize` (currently 4)
  - [ ] Total `cs ssh` invocations per poll <= `count(sandboxes) + 1`

Adjust `crafting.New()` if the default pool size starves a busy
workspace, or pump up the poll interval in `ui.Model.Init`.
