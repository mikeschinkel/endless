# E-1758 — Worktree anomaly check: `worktree check` (agent) + `session status` detail (human)

## Context

The handoff prompt tells agents to "report git/worktree side-effects (uncommitted
changes, dangling tags, stray artifacts), the landed-vs-worktree delta" — an
enumeration of categories. Naming an empty category makes the agent fill it with
"none," so a clean handoff produces "nothing to report" dressed as reporting.
Two of the named categories aren't even real: endless **never creates git tags**
("dangling tags" is a non-problem), and a pre-land branch **normally** has commits
ahead of main (the work), so "landed-vs-worktree delta" is the expected state, not
an anomaly.

Fix: compute the *real* anomalies so a clean state is **silence**, not prose.
Surface it two ways (Mike's steer — both, not either/or):
- `endless worktree check` — the agent runs it at handoff; prints nothing when
  clean, terse anomaly lines otherwise.
- `endless session status` — the same anomalies rendered inline so Mike sees git
  state directly (today it shows only the coarse ◆ dirty marker, not *what*).

Both consume ONE definition of "anomaly" so they never disagree.

## Anomaly set (single source of truth)

Report ONLY genuine handoff anomalies:
- Uncommitted or untracked **user** files — `git status --porcelain`, partitioned
  to exclude endless's own auto-managed files (the globs `_git_status_partition`
  already uses: db-ledger, plans/outcomes/analyses/decisions mirrors, verbs.jsonl,
  companion, `.claude/settings.json`). Endless-managed dirt alone → still clean.
- HEAD **detached**, or not on the worktree's companion `branch`.
- The worktree (or a sibling e-NNN dir) **prunable / stale-locked / leaked**.

Explicitly NOT anomalies (stay silent):
- Commits ahead of main (expected before land).
- Git tags (endless never creates them).

Empty anomaly list ⇒ clean ⇒ no output.

## Mechanism

**Shared core (Go).** Generalize `monitor.taskWorktreeDirty` (currently a bool that
collapses dirty-or-unlanded) into `monitor.WorktreeAnomalies(...)` returning a typed
list (kind + short detail), computed via the existing `runGit`, the reaper's
`git worktree list --porcelain` prunable/lock inspection, and a
centralized auto-managed-file glob (port the list currently living behind Python's
`_git_status_partition` into one shared Go definition so both languages agree). The
"unlanded commits" branch of `taskWorktreeDirty` is dropped from the anomaly list
per the silence rule (the ◆ marker's unlanded meaning can stay as-is for the
navigation view).

**Surface 1 — `endless worktree check` (agent).** New subcommand in the Python
`worktree` group; resolves the current worktree from cwd (reuse `current_worktree`
/ `worktree_root_for_cwd`), shells to a Go subcommand (e.g.
`endless-go session-query worktree-anomalies --task-id N`, matching the existing
session-query pattern), prints one line per anomaly, nothing when empty. Exit 0 when
clean, non-zero when anomalies (scriptable; the agent relays stdout verbatim).

**Surface 2 — `session status` detail.** Extend the Go render
(`SessionStatusRow` / `AnnotateSessionStatusDirty` in `internal/sessionstatuscmd` +
`internal/monitor`) so the ◆ marker expands to the same `WorktreeAnomalies`
breakdown for the focal worktree — silent (unchanged output) when there are none.

## Scope boundary

E-1758 delivers the shared core + both surfaces + the glob centralization. It does
NOT rewrite the handoff templates (that is E-1759, which will wire `worktree check`
into all six `internal/templatecmd/templates/handoff/*.tmpl` and strike the
"dangling tags" / "landed-vs-worktree delta" language) and does NOT add the lint
gate (E-1760).

## Deliverable — verify script

Generate `tests/tasks/e-1758-verify.sh` (isolated env + a real worktree, shape per
`tests/tasks/e-1747-verify.sh`). Assert:
- clean worktree → `endless worktree check` prints nothing, exit 0.
- an untracked USER file → printed, exit non-zero.
- an endless auto-managed file alone (e.g. a `.endless/outcomes/E-NNN.md`) → still
  clean (partitioned out).
- commits ahead of main with an otherwise-clean tree → still clean (silence rule).
- detached HEAD → flagged.
- `session status` shows the same detail behind ◆ when dirty, and nothing extra
  when clean.
Plus Go unit tests for `WorktreeAnomalies` covering each anomaly kind + the clean
case.

## Verification (handoff to Mike)

Set `unverified`; run `esu && ./tests/tasks/e-1758-verify.sh`.

## Notes

- Reuses E-1701's ◆-marker git-cost pattern (`taskWorktreeDirty`) and the Python
  `worktree` inspection helpers; the one genuinely new asset is the typed
  `WorktreeAnomalies` core and its single auto-managed glob definition.
- Once landed, unblocks E-1759 (templates call `worktree check`) and complements
  E-1760 (the lint gate catches ceremony the template can't).
