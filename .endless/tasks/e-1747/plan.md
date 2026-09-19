# E-1747 — Version-control all multiline content as committed mirror files

## Context

Today only a task's plan (`tasks.text`) is mirrored to a git-tracked file
`.endless/plans/E-NNN.md` (materialized at worktree birth, re-mirrored on each
`--text` write, committed on the worktree branch so it rides `worktree land` to
main). Every OTHER multiline document field — `tasks.outcome`, `tasks.analysis`, and
decision bodies — lives only in the DB and in the committed db-ledger JSONL. If a DB
row is lost, or an authoring scratch file is referenced instead of embedded (the
corruption class E-1744 addresses), that content has no human-readable, git-backed
copy to recover from or to see when reviewing the repo on GitHub.

This is the durability **belt** (belt-and-suspenders with E-1748's
`/tmp`→`.endless/tmp` scratch dir): content that reached the DB should also exist as
a committed file. Excludes short metadata (`description`, `title`).

Mike's decisions (this session): decisions mirror to the **current worktree if
`decision add` runs inside one, else to main**; **backfill of existing rows is a
separate follow-up task**, not part of E-1747.

## Existing machinery to reuse

- `worktree_cmd._materialize_plan_file(task_id, wt)` — birth-time; reads `tasks.text`
  via `endless-go session-query task-text`, writes `<wt>/.endless/plans/E-NNN.md`, commits.
- `worktree_cmd._commit_plan_file_in_worktree(wt, task_id, subject)` — scoped
  `git add` + `git commit -o <rel>`; idempotent no-op when unchanged.
- `task_cmd._mirror_plan_to_worktree(task_id, content)` — write-time; mirrors into
  `_worktree_for_task(task_id)` if a worktree exists, else DB-only. Direct content
  pass (no read-back).
- Go read helper `internal/sessionquerycmd` `task-text` → `monitor.TaskText(id)`.
- Global main commit: `internal/events/commit.go` `commitPaths()` /
  `CommitLedgerSegment()` — commits directly to the **main checkout**
  (`ensureMainCheckout` refuses a linked worktree; strips `GIT_DIR`-family vars).

## Field → path map

| Field            | Path                          | Scope                     |
|------------------|-------------------------------|---------------------------|
| `tasks.text`     | `.endless/plans/E-NNN.md`     | worktree (existing)       |
| `tasks.outcome`  | `.endless/outcomes/E-NNN.md`  | worktree (new)            |
| `tasks.analysis` | `.endless/analyses/E-NNN.md`  | worktree (new)            |
| decision body    | `.endless/decisions/ED-NNN.md`| current worktree, else main (new) |

No `.gitignore` change — these dirs are tracked content, like `.endless/plans/`.

## Implementation

### 1. Go — generalize the field read (`internal/`)
- Add `monitor.TaskField(id int64, column string) (string, error)` with a whitelist
  (`text`, `outcome`, `analysis`) → `SELECT COALESCE(<col>,'')`. Refactor
  `TaskText` to delegate (or keep it).
- Add `session-query task-field --id --name <text|outcome|analysis>` case in
  `sessionquerycmd/session_query.go` (mirror `runTaskText`); update usage text.

### 2. Go — main commit for decision docs (`internal/events/commit.go`)
- Add exported `CommitDoc(projectRoot, relPath, subject string) error` →
  `commitPaths(projectRoot, []string{relPath}, subject, "<dir>/*.md")`.
- Expose via `endless-go` (small subcommand, e.g. under `event` or a new
  `commit-doc`) so Python can request the main-checkout commit and inherit the
  `ensureMainCheckout` + git-redirect-var safety instead of re-implementing it.

### 3. worktree_cmd.py — field-agnostic worktree mirror + birth seeding
- Generalize `_commit_plan_file_in_worktree` → `_commit_doc_in_worktree(wt,
  rel_path, subject)`; keep `text` behavior identical.
- Replace `_materialize_plan_file` with `_materialize_task_docs(task_id, wt)` that
  loops `(name, subdir, prefix)` over
  `text→plans`, `outcome→outcomes`, `analysis→analyses`, reading each via
  `session-query task-field` and writing+committing every non-empty one. Commit
  subjects: `Endless: add <plan|outcome|analysis> for E-NNN`. Called from
  `create_task_worktree` (replaces the single plan call at line ~1150).

### 4. task_cmd.py — write-time mirrors (direct content pass)
- Generalize `_mirror_plan_to_worktree` → `_mirror_doc_to_worktree(task_id, subdir,
  prefix, content, verb)`; keep a thin `text` caller.
- Add mirror calls wherever a doc field is written:
  - `update_plan`: `--outcome` → outcomes, `--analysis` → analyses (beside the
    existing `--text` mirror).
  - `complete_item`, `assume_item`, `mark_completed_item`, `decline_item`,
    `replace_task`: mirror `--outcome` after emitting the status event.

### 5. decision_cmd.py — decision body mirror (current worktree, else main)
- Add `_current_endless_worktree() -> Path | None`: resolve cwd's git toplevel; if
  it is an endless-managed worktree (`.endless/worktrees/e-NNN` + `worktree.json`
  companion), return it, else None.
- In `add_decision`, after `new_id` is known and `description` is non-empty:
  - worktree found → write `<wt>/.endless/decisions/ED-NNN.md`, commit on the
    worktree branch via `_commit_doc_in_worktree` (rides that worktree's land).
  - else → resolve the project's main root, write there, commit to main via the Go
    `CommitDoc` helper.
  - Subject: `Endless: add decision ED-NNN`.

### 6. Follow-up task (file during implementation, don't implement)
- `endless task add "Backfill mirror files for existing outcome/analysis/decision
  rows" --cleans-up E-1747` — one-shot pass to generate mirrors for rows that
  predate this machinery. Confirm wording with Mike before filing.

## Deliverable — verify script

Generate `tests/tasks/e-1747-verify.sh` (self-contained, `--db sandbox`, shape/
output per `tests/tasks/e-1577-verify.sh`: `endless(){ uv run endless "$@" --db
sandbox; }`, `add_task_get_id`, `assert_*`, `summary`), committed with the
implementation. Because the mirror only fires when a worktree exists, the script
creates a real worktree (claim) so the assertions have a target. It covers:
- `--outcome` and `--outcome-file` on a task WITH a worktree → `.endless/outcomes/
  E-NNN.md` written AND committed with the content.
- `--analysis` → `.endless/analyses/E-NNN.md` written+committed.
- decision body → `.endless/decisions/ED-NNN.md` written+committed (worktree branch
  when run from inside one).
- Regression: `tasks.text` → `.endless/plans/E-NNN.md` still works unchanged.
- No-worktree task → DB-only, no file, no crash.

Also extend `tests/test_plan_file_to_worktree.py` with outcome/analysis mirror + a
`_materialize_task_docs` multi-field test, following its existing monkeypatch/git-init
fixtures.

## Verification (handoff to Mike)

Set `unverified` and tell Mike to run exactly:

```
esu && ./tests/tasks/e-1747-verify.sh
```

MANUAL (only if a case can't be scripted cleanly in the sandbox): land a worktree and
confirm the outcome/analysis mirrors arrive on `main` alongside the plan mirror, and
that a fresh claim/spawn seeds all mirrors at worktree birth.

## Build/run notes
- `just build` after Go/templ changes; the worktree's `bin/endless-go` is what the
  Python layer shells to.
- `just test` for the Python suite.
