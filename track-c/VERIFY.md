# VERIFY.md — track-c real-infra checklist

The build session ran against the mock only; the items below are the
assumptions baked into `backend/crafting.go` and the launcher that need a
live Crafting workspace and `cs`/`claude` to confirm. Each item lists the
exact command to run and the assumption it validates. Order matters — the
earliest failures change everything downstream.

## 1. Supervisor survives SSH disconnect (the central assumption)

This is the load-bearing piece. If it fails, add **one** per-sandbox
persistence anchor — not a per-agent muxer.

```sh
# pick a sandbox; replace <sb> below
cs ssh <sb> -- claude --bg --name "survive-test" "Echo a line every 5s until told to stop."
# note the printed session id, then exit the ssh process (Ctrl-D)
sleep 30
cs ssh <sb> -- claude agents --json | jq '.sessions[] | select(.name=="survive-test")'
# Expect: state="working", and the session id matches.
```

Pass criteria: the session appears, state is `working`, and stdout/stderr
is going somewhere (`claude logs <id>` shows recent output).

Failure response: add a workspace daemon / lifecycle entry that holds the
supervisor alive (Crafting workspace `services:` block running `claude
serve` or equivalent), or a single dtach-held supervisor process per
sandbox. Re-run the test.

## 2. Suspend / respawn round trip

```sh
cs ssh <sb> -- claude --bg "Iterate on this for a while."  # note id
# suspend sandbox <sb> via cs (or Crafting UI)
sleep 60
cs ssh <sb> -- :   # forces resume; should block until Running
cs ssh <sb> -- claude respawn --all
cs ssh <sb> -- claude agents --json | jq '.sessions[] | select(.id=="<id>")'
# Expect: state restored to working/idle (not failed/stopped) and
# `claude attach <id>` lands on the same conversation.
```

If `on_resume` lifecycle hook is configured per spec C, the `claude
respawn --all` should fire automatically on the resume transition;
running it manually here removes that dependency for the test.

## 3. `claude agents --json` field-name shape

The decoder in `backend/crafting.go` assumes (`claudeAgents` struct):

```
.sessions[].id, .name, .state, .last_activity, .pr, .age_seconds
```

Confirm against real output:

```sh
cs ssh <sb> -- claude agents --json | jq .
```

Map any deltas into `backend/crafting.go::claudeAgents`. Likely mismatch
candidates: `last_activity` may be camelCase / different format, `age_seconds`
may not exist (compute from `last_activity`), `pr` may be nested in a
metadata block.

## 4. `cs list --json` shape

The decoder assumes:

```
.sandboxes[].id, .name, .state ("running" | "suspended" | ...)
```

Confirm:

```sh
cs list --json | jq .
```

Update `backend/crafting.go::csList` and the `state != "running"` filter
in `CraftingBackend.List` to match the real values.

## 5. Activity-probe exit-code convention

The probe script in spec C uses `0`=active, `1`=inactive. Confirm what
Crafting actually reads:

```sh
# inside a workspace, write a probe that returns 0 and see if the workspace
# stays active; flip to returning 1 and see if it suspends.
cat > /tmp/probe.sh <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
chmod +x /tmp/probe.sh
# register /tmp/probe.sh as the workspace's activity probe and observe.
```

Once confirmed, wire the real `agent-probe.sh` from spec C §"Activity probe"
into the workspace definition.

## 6. Agent view through Vertex AI

Claude Code's agent view shipped recently and earlier versions were not
available in Bedrock/Vertex/Foundry environments. Personal Max-plan use
isn't a proxy for the Compass / Vertex setup.

```sh
# in a Compass-style sandbox configured for Vertex
claude agents --json
```

If the command errors / returns empty / says "not available in this
environment", the path forward is either to wait for shipping support or
to fall back to spec A/B's self-built dtach approach.

## 7. Attach over `cs ssh -t`

The right-pane content window does:

```sh
cs ssh -t <sb> -- claude attach <id>
```

Spot-check this works non-interactively (well, with a tty allocated):

```sh
cs ssh -t <sb> -- claude attach <id>     # interactive; ^D to detach
# Then in another shell:
cs ssh <sb> -- claude agents --json | jq '.sessions[] | select(.id=="<id>") | .state'
# Expect: the session is still running.
```

Also verify the spec-C reconnect wrapper in `internal/attach/attach.go::Real`:
killing the local SSH client mid-attach should leave the session running.

## 8. Polling cost / quota fan-out

With N sandboxes, each `List` makes 1 `cs list --json` plus N `cs ssh ...
claude agents --json` calls every `pollInterval` (default 2s) bounded at
`Concurrency` (default 8) workers. On a real fleet:

```sh
time cs list --json
time cs ssh <sb> -- claude agents --json
```

If a single per-sandbox poll is > ~1s, raise `pollInterval` and/or lower
`Concurrency`. If many sandboxes are suspended, ensure
`CraftingBackend.List` is correctly elided them (see the `sb.state != "running"`
filter in `crafting.go`).

## 9. Dispatch parsing

`CraftingBackend.Dispatch` returns the last whitespace-delimited token of
the `claude --bg` output as the session id. Confirm format:

```sh
cs ssh <sb> -- claude --bg --name "dispatch-test" "Hello." 
```

If the printed line is not e.g. `started session abc123`, adjust the
parser in `crafting.go::Dispatch`.
