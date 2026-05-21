# Workflow

How to make changes in this repo without breaking the apples-to-apples
comparison between the two variants. Read `AGENTS.md` first.

## Where does this change belong?

| Kind of change | Branch |
|---|---|
| `backend/backend.go` or `backend/mock.go` (the shared seam) | `main` |
| root README, `AGENTS.md`, `specs/`, `docs/` | `main` |
| anything under `track-c/` other than the shared seam | `track-c-tmux` |
| anything under `track-d/` other than the shared seam | `track-d-bubbleterm` |

If you're not sure: shared changes go on `main`. It's cheaper to
over-propagate than to introduce divergence between the two variants' seeds.

## Recipe: shared change on `main`

1. From repo root on `main`, make the change. Update **both**
   `track-c/backend/` and `track-d/backend/` copies of any file that's
   duplicated by design (the interface, the mock).
2. Sanity-check both seed scaffolds:
   ```sh
   (cd track-c && go build ./... && go vet ./... && go run .)
   (cd track-d && go build ./... && go vet ./... && go run .)
   ```
   The roster printed from `go run .` should match what you intended.
3. Commit on `main`.
4. Rebase each feature branch onto the new `main` (in a worktree or a
   regular checkout — either works):
   ```sh
   git -C <track-c-tmux checkout> rebase main
   git -C <track-d-bubbleterm checkout> rebase main
   ```
5. In each rebased branch, run the variant's full build (and tests, where
   they exist):
   ```sh
   # tmux variant
   cd <track-c-tmux checkout>/track-c
   devbox run -- go build ./...
   devbox run -- go vet ./...

   # bubbleterm variant
   cd <track-d-bubbleterm checkout>/track-d
   devbox run -- go build ./...
   devbox run -- go vet ./...
   devbox run -- go test ./...
   ```
6. If a test or doc on the feature branch asserts on something you just
   changed, fix it on the feature branch and commit there. The obvious
   candidate is `track-d/ui/model_test.go`, which asserts on a literal
   sandbox name from the mock.
7. Push everything:
   ```sh
   git push origin main
   git push --force-with-lease origin track-c-tmux
   git push --force-with-lease origin track-d-bubbleterm
   ```

## Recipe: variant-only change

1. Check out the variant's feature branch (`track-c-tmux` or
   `track-d-bubbleterm`).
2. Make the change inside `track-c/` or `track-d/` respectively. Don't
   touch the shared seam (`backend/backend.go`, `backend/mock.go`) — if
   you need to, that's a shared change, not a variant-only one.
3. Build, vet, test as above (in the variant's directory).
4. Commit on the feature branch.
5. `git push --force-with-lease origin <branch>` (force-with-lease only
   needed if you rebased; a fast-forward `git push` is fine for an
   appended commit).

## Common pitfalls

- **Forgetting to update both `backend/mock.go` copies.** They're meant to
  be byte-identical. If you change one and not the other, you've broken
  the comparison. Always edit both, then `diff -u track-c/backend/mock.go
  track-d/backend/mock.go` should show no output.
- **Forgetting to rebase a feature branch.** If you only update `main` and
  don't rebase, the feature branches stay on the old seed. They'll still
  build, but the apples-to-apples premise quietly slips.
- **`track-d/ui/model_test.go` failing after a mock rename.** It contains
  `if !strings.Contains(v.Content, "payments-api")`. Update the literal
  whenever the sandbox names move.
- **Force-pushing `main`.** Don't. `main` is the integration point;
  feature branches rewrite freely, `main` doesn't.
- **Touching `track-c/` from the `track-d-bubbleterm` branch (or vice
  versa).** Each feature branch only owns its own variant. Cross-variant
  edits should be rare; if you need them, do it on `main` so both inherit.
