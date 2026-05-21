# NOTES — Track D build session

## Fidelity spike outcome

**Verdict: bubbleterm passes; no fallback to `charmbracelet/x/vt`
needed.** The build session has no TTY so the *interactive* spike at
`cmd/spike/` could not be exercised, but the headless spike at
`cmd/spike-headless/` drove the emulator with a representative ANSI
stream and the resulting frame matched the input exactly:

```
 0 | hello world
 1 | 256-color orange
 2 | true-color violet
 4 |          [saved]
 9 | wide: 漢字 emoji: 🌟 box: ╔═╗
19 | cursor parked here
```

That covers, deterministically: alt-screen enter, SGR 1/36/38;5;208/
38;2;r;g;b, ED (clear 2J), CUP (cursor positioning), DECSC/DECRC
(save/restore cursor), and wide CJK + emoji + box-drawing runes
landing at the expected columns. Damage tracking reports.

Remaining unknowns are all *interactive* (cursor shape, OSC title,
mouse passthrough, bracketed paste round-trip) and have to be judged
against a real Claude session — that's in `VERIFY.md` item 0.

The library is unmaintained-looking (`taigrr/bubbleterm@v0.2.0` is
small and pinned), but the core is a `charmbracelet/x/vt`
wrapper, so even a fork-and-fix is plausible if it ever breaks.

## Module path quirk

`spec-d` says `charm.land/bubbletea/v2` etc., and that's what the
modules' own `go.mod` declares. `go get github.com/charmbracelet/...`
fails with `module declares its path as: charm.land/...`. The fix is
to import from `charm.land/`. Documented here so the next session
doesn't burn time rediscovering it.

## What's stubbed in CraftingBackend

Everything compiles, vets clean, and has the right shape, but the
following are best-effort decodes against unverified wire formats:

1. **`cs list --json` shape.** The decode tries a top-level array,
   then `{sandboxes:[]}`, then `{items:[]}`, with only `id` actually
   used. Confirm and tighten.
2. **`claude agents --json` shape.** Decode is `{sessions:[{id,
   name, state, pr, age_seconds}]}` or a bare array. Field names
   recently shipped; verify per `VERIFY.md` item 3.
3. **Dispatch return value.** `claude --bg` is assumed to print the
   new session id on the first non-empty stdout line. Untested.
4. **Stop / Respawn.** Calls `claude stop <id>` and
   `claude respawn --all` over `cs ssh`. Exit-code contract not
   independently verified.
5. **Activity probe.** The probe script is in the spec but not yet
   wired into the binary — there is nothing for it to wire into
   except a deploy step, so it stays in `VERIFY.md` as a one-liner
   to install on the workspace.
6. **`on_resume` hook.** Same — install-time step, not part of the
   binary.
7. **Push-based liveness (hooks).** The spec mentions optional
   `Notification`/`Stop` hooks posting to an in-process listener;
   not implemented. The 2s poll covers the baseline.
8. **Reconnect on stream drop.** `cmd.Wait()` is not monitored for
   the ssh subprocess; if it dies the emulator goes quiet but no
   reattach is attempted. Spec calls this out as nice-to-have, not
   required for Definition of Done.

## Decisions made

- **No bubbles/v2 `list.Model`.** Rolled a custom sidebar so the
  layout (per-sandbox grouping, state markers, attached dot, cursor
  arrow) is fully ours. The spec said `bubbles/v2` "for the flat
  list" but didn't require it, and the chrome comparison with track-c
  is more interesting when neither side hides behind a default
  delegate. The whole sidebar render is ~70 LOC.
- **One bubbleterm.Model per attached session, kept in a map.** That
  way switching to a different row and back preserves both emulators'
  state without any explicit save/restore — Definition of Done #5.
  Memory cost is bounded by the user's curiosity; if it grows
  unreasonably, evict on `claude attach` close.
- **Centralized `resize()` function.** Spec calls this out as the
  crux. All three sizes — content rect, `term.Resize`, and
  `pty.Setsize` on the stream — are pushed from one method, off the
  one `WindowSizeMsg`. The PTY resize is gated on a type-assert
  (`interface { Resize(cols, rows int) error }`) so the mock backend
  (which is just pipes) is silently skipped.
- **`WindowSizeMsg` is NOT forwarded to bubbleterm.Update.** Its
  default handler calls `emu.Resize(w-2, h)` for the whole window;
  we already size each model to the content rect explicitly, so the
  forward would double-resize and shrink the content by 2 columns
  per resize.
- **Focus key is `tab`.** Spec said "single toggle key"; `tab` is
  ergonomic. `ctrl+c` and `ctrl+\` are always-on quits because the
  attached terminal probably wants `q` for its own purposes.
- **Auto-poll left ON in bubbleterm.** The model self-perpetuates a
  poll chain. We could externalize this for finer control over CPU
  but the cost is fine at 30 FPS and the simplification (no extra
  ticker) is worth more.

## Open questions hit while building

- `lipgloss.Width` vs byte length for padding the sidebar — used
  `lipgloss.Width` so styled cells don't break the column. Cells with
  emoji or CJK in *names* would still be miscounted (display width vs
  cells) but mock and real backends so far have ASCII names.
- The `bubbleterm.View()` return is `tea.View`, not a string;
  `.Content` is the string. Pulled it out manually so we can
  hand-pad to the content rect.
- bubbletea v2 dropped `WithAltScreen()` and `WithMouse*()` as
  program options; they're View-time properties now
  (`v.AltScreen = true`, `v.MouseMode = tea.MouseModeAllMotion`).
  Spike and main both use the new form.
- `tea.KeyMod` has `Contains()` (via the `ultraviolet.KeyMod` type
  alias), which is cleaner than `==` for ctrl-detection.

## Things a reviewer should look at first

1. **`ui/model.go` `resize()` (lines ~245-285).** That's the size
   pipeline the spec calls "the crux"; if it's right, the rest
   follows.
2. **`cmd/spike-headless/main.go` output.** Run it and look at the
   frame; that's the strongest evidence the variant is viable.
3. **`ui/model.go` `attached map`.** That's the "switch without
   losing state" trick; one line, makes Definition-of-Done #5 free.
4. **`backend/crafting/crafting.go` `Attach()`.** The local PTY
   wrapping is the only part of the live backend that has to be
   *exactly* right; everything else is JSON we can fix when we see
   the real shapes.
