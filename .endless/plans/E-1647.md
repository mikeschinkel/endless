# E-1647 — Flat type-agnostic respawn handoff template

**Epic:** E-1644 · **Blocks:** E-1645 · **Layer:** Go template + Python render glue

## Goal
A single `handoff/respawn.md.tmpl` used for *every* reopen, regardless of task type. The
four existing templates (`task`/`bug`/`research`/`epic`) are *initial-spawn* instructions
that drive toward an end-state; a reopened session has no defined end-state yet, so it gets
a distinct, interrogative handoff that says "get into the worktree, then ask the user why
we're here."

## Files
- **New:** `internal/templatecmd/templates/handoff/respawn.md.tmpl` (embedded via the existing
  `//go:embed templates` at `internal/templatecmd/template.go:34-35`).
- **Render glue:** `src/endless/task_cmd.py:3759-3821` (`render_handoff(...)`). Add a path so a
  reopen renders `handoff/respawn` instead of `handoff/<type>`, and passes three NEW vars
  (below). The Go side (`endless-go template render`, `template.go:64-113`) needs no change.

## Template variables
Existing: `spawned_id, label_prefix, title, spawner_task, return_anchor, worktree_path, branch,
child_count, children_state, bg`. **Add three** (supplied by the E-1645 reopen path):
- `restore_case` — `reused` | `rebuilt-off-main` | `recovered-post-drop`.
- `prior_outcome` — the task's `outcome` field (may be empty).
- `last_status_snapshot` — rendered markdown of the latest `session_statuses` row (may be empty).

## Template content (requirements)
1. **Lead with** `​/cd {{.worktree_path}}` — absolute path, first instruction.
2. State the **restore case** and that this task/session was **previously resolved**; include
   `{{.prior_outcome}}` and `{{.last_status_snapshot}}` as **read-only context**.
3. **The pivot:** "This was previously worked on / landed. **Ask the user what their goal is
   for reopening it before doing anything else.** Then run `endless guide` and
   `endless task show {{.spawned_id}} --text`."
4. Keep the lean drive-to-completion rules (don't `worktree land`/`drop` without asking; file
   drive-by work as separate tasks with `--cleans-up`).

## Notes
- Self-dev renders straight from embedded (`template.go:86-94`) — no on-disk `.tmpl` to commit.
- Per-type respawn variants are out of scope; revisit only if the flat handoff proves too blunt.

## Verification — create a per-task verify script
Create `tests/tasks/e-1647-verify.sh`, modeled on the shape + output of
`tests/tasks/e-1624-verify.sh` and the E-1596 verify-suite direction: bash, `set -u`,
`section`/`report_pass`/`report_fail`/`summary` helpers, `cd` to repo root via
`git rev-parse --show-toplevel`, ensure `go.work` (regenerate via `just go-work-init` if
missing), exit **0 = ALL PASSED / 1 = any failure / 2 = env/setup error**. Drive the REAL
render path — don't re-implement templating.

The script asserts (each a named check):
- `internal/templatecmd/templates/handoff/respawn.md.tmpl` exists.
- Rendering the template via the real path exits 0 and substitutes all three new vars with no
  `<no value>` left:
  `echo '{"spawned_id":1647,"title":"X","worktree_path":"/abs/p","restore_case":"rebuilt-off-main","prior_outcome":"prev","last_status_snapshot":"snap"}' | endless-go template render handoff/respawn`
- The rendered output contains the `​/cd /abs/p` line.
- The rendered output contains the interrogative pivot (the "ask the user … goal" line), proving
  it is the respawn template, not a type template.

Re-runnability (the self-dev sandbox is NOT wiped between runs):
- Never seed a fixed value into a UNIQUE column — derive seed values from freshly-allocated ids.
- Don't pipe seeding output to /dev/null; capture it and `exit 2` loudly on any setup/seed error.
- Run the script at least TWICE before declaring it done.

**Verify-handoff — tell the user to verify with (only) this:**
```
esu && ./tests/tasks/e-1647-verify.sh
```
