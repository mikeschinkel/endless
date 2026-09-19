# E-1754 — Backfill mirror files for pre-existing doc content — PLAN

## Context

E-1747 mirrors `tasks.{text,outcome,analysis}` and decision bodies to committed
`.endless/{plans,outcomes,analyses,decisions}/…md` files, but only for content
written from now on. Rows that already hold outcome/analysis (and existing
decisions, and any task whose text never got a plan file) have no mirror yet.
This is the one-shot pass that fills those gaps so the durability belt covers
history.

## Approach

A one-shot command, run FROM the E-1754 worktree, reconciles every document
field for the worktree's project into committed mirror files **on the E-1754
worktree branch** — the files then ride the normal `worktree land` to main. No
direct-to-main commits; no per-file commit storm.

Fill gaps only: write a mirror **only when the target file is absent**. It never
overwrites an existing file, so it can't reconcile drift or rewrite the hundreds
of plan files already on main — and re-running is trivially a no-op.

## Mechanism — a persisted one-off migration artifact, NOT a CLI command

Mike's steer: no user-facing verb (dead surface after its one run). DB reads are
Go-only (E-894), so the write logic is Go — but shaped as a persisted one-off
that never enters either binary's command surface:

- A `//go:build ignore` `package main` program (same build-excluded form as the
  `internal/schema/changes/e-NNN-*.go` migrations), run explicitly via
  `go run <file>`. `go build ./...` skips it; `endless`/`endless-go` never grow a
  subcommand. It does NOT use the schema-changes runner — that runner is
  DDL-specific (wraps each change in a DB transaction, records `_schema_version`);
  this one reads the DB read-only and mutates files + git.
- Home: `.endless/migrations/e-1754-backfill-doc-mirrors.go` — co-located with the
  other committed endless artifacts (`plans/`, `outcomes/`, `decisions/`), since a
  backfill is endless bookkeeping, not product code, and this keeps `internal/` /
  `cmd/` free of one-off maintenance scripts. It is still under the Go module root,
  so `go run` compiles it with `internal/…` imports; `//go:build ignore` excludes
  it from `go build ./...`. (NOT `internal/schema/changes/` — that is the
  DDL-via-runner home and the land dispatcher would try to auto-apply it.)

What it does, run from the E-1754 worktree:

1. Reads, for the worktree's project: tasks with non-empty `text` / `outcome` /
   `analysis`, and decisions with a non-empty body. One bulk query each (reuse
   `internal/monitor` DB access; the column→subdir map mirrors E-1747's
   `_TASK_DOC_FIELDS`: text→plans, outcome→outcomes, analysis→analyses; decision
   body→decisions with the `ED-` prefix).
2. For each, if `<wt>/.endless/<subdir>/E-NNN.md` (or `ED-NNN.md`) is **absent**,
   writes it (creating the subdir). Present → skip (gap-fill, never overwrites).
3. Stages all newly-written paths and makes ONE commit on the worktree branch:
   `Endless: backfill doc mirrors (N files)`. Plain `git -C <wt> add/commit` —
   NOT `events.commitPaths`, which refuses linked worktrees (`ensureMainCheckout`).
   Zero new files → no commit.

Re-runnable and idempotent (gap-fill means a second run finds every file present
and writes nothing). Precedent for a persisted, re-runnable backfill driver:
`tests/tasks/e-1719-backfill.sh`.

## Scope

- Fields: all four — text, outcome, analysis, decision bodies.
- Project: the worktree's project only (endless). Other projects, if they adopt
  the committed-mirror convention, run their own backfill later — out of scope.

## Deliverable — verify script

Generate `tests/tasks/e-1754-verify.sh` (isolated env, shape per
`tests/tasks/e-1747-verify.sh`). Seed a project + a git worktree; create tasks
carrying outcome/analysis (and text) with NO per-task worktree, plus a decision;
run the backfill from the worktree via
`go run .endless/migrations/e-1754-backfill-doc-mirrors.go`. Assert:
- the expected `.endless/{plans,outcomes,analyses}/E-NNN.md` and
  `.endless/decisions/ED-NNN.md` appear in the worktree AND are in one commit.
- a task/decision with empty fields produces no file.
- an already-present mirror is left untouched (gap-fill, not overwrite).
- re-running the backfill makes no second commit (idempotent).

## Verification (handoff to Mike)

Set `unverified`; tell Mike to run `esu && ./tests/tasks/e-1754-verify.sh`.
MANUAL: in the real E-1754 worktree, run the backfill; `git log`/`git status`
show one `backfill doc mirrors` commit with the expected new files; then land
E-1754 normally and confirm the mirrors arrive on main.

## Notes

- Reuses E-1747: the field→subdir convention and file layout; `internal/monitor`
  for reads. The genuinely new piece is the bulk read + single worktree-branch
  commit, packaged as a build-excluded one-off (no command surface).
- Not a legacy command: nothing is added to `endless` / `endless-go`. The
  artifact persists like a dated migration and is re-runnable via `go run`.
