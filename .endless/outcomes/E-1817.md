# CLAUDE.md audit (E-1817)

The proposed replacement text is at the bottom of this outcome, in full.

**385 lines → 58. ~6,200 tokens → ~545. 91% cut**, charged against every session
in this repo unconditionally.

## The rule the file now follows

CLAUDE.md states WHAT, never WHY. No rationale, no history, no mechanism, no
task IDs, no pointers to decisions. Workflow → `endless guide`. Product
invariants → code comments beside what enforces them (ED-1564).

Everything cut was one of: already in the guide, already in a code comment,
rationale, or stale.

## Three live defects, fixed in the proposal

1. **"Run `just install` from the main checkout, never from a worktree"** is
   false and harmful. E-1036 made `just install` worktree-aware; it detects a
   worktree and does a scoped install that never touches the global one. The
   rule steers agents away from the supported path.
2. **`bin/endless-hook` and `/usr/local/bin/endless-hook` do not exist.**
   E-1367 consolidated into `endless-go`; `justfile:104` deletes the old symlink
   on every install.
3. **`endless-sandbox destroy e-NNN` does not exist.** Same consolidation. It is
   `endless-go sandbox destroy e-NNN`.

## Applied under this task

- **The lifecycle section is deleted.** It was a verbatim duplicate of the
  canonical lifecycle in `endless guide`. `tests/test_status_lifecycle_sync.py`
  hardcoded the three copies, so it could not tell "deleted on purpose" from
  "drifted" — `CLAUDE.md` dropped from `_COPIES`. CLAUDE.md: 385 → 340 lines.
- **`docs/guide/reference.md`** listed `/usr/local/bin/endless-hook`, `-event`
  and `-tmux` as separate binaries (defect 2 above), in the *shipped* guide.
  Replaced with `endless-go`; also corrected the Python CLI to `~/.local/bin`.
- **`docs/status-lifecycle.mmd`'s header** named CLAUDE.md as a copy and cited
  `tests/tasks/e-1648-verify.sh`; E-1889 moved that assertion to
  `tests/test_status_lifecycle_sync.py`. Fixed, and `README.md` /
  `docs/guide/index.md` re-synced.
- **`internal/sandboxcmd/seed_worktree.go`** claimed the git-dir/git-common-dir
  discriminator was "documented in this project's CLAUDE.md". It never was.
- **ED-1541** (memory-off) named the retired `~/.claude/LESSONS.md` and claimed
  "Scope: global", which was never enforced — `~/.claude/settings.json` sets
  `autoMemoryEnabled: true`; only the endless repo sets it false.

`just test`: 1499 passed. `go build/vet ./...`: clean. `just guide-check`: OK.

## Not applied

The rest of the proposal — the WHAT-only rewrite of the seven remaining sections
— is below, unapplied, for review.

**E-1816 is not closed by this.** The wording it flags ("background sessions may
pick up only `ready` work") is also at `docs/guide/index.md:85`. That reword is
E-1816's own work; doing it here would silently close another open task.

## Correction to this audit's own method (2026-08-20)

This audit routed cut material to "code comments beside what enforces them" and
justified deleting sections by citing comments as authoritative. Mike: comments
an agent wrote without his input get treated as gospel; **accepted decisions are
the source, code comments are to be viewed with suspicion.** ED-1564 is updated
accordingly.

Two consequences, both real:

- **I did it in this audit.** I called `internal/monitor/project_path.go`'s
  comment "richer than CLAUDE.md" and used it to justify deleting the E-2002
  section. That comment says "nothing writes a tilde into projects.path."
  **Accepted ED-1562 says stored paths ARE home-relative, with a tilde prefix.**
  The comment is right about today's code and wrong about the decided design
  (E-2011 is the open implementation). CLAUDE.md's E-2002 section is stale
  against an accepted decision for the same reason.
- **ED-1564 is still `proposed`, not `accepted`.** This audit has leaned on it
  as settled throughout — the same error one level up. Only Mike accepts a
  decision. Nothing here is authoritative until he does.

Neither changes the deletions: those sections were cut because CLAUDE.md states
WHAT for the agent, not because a comment covered them. But if the harness rules
(E-1962) or the path rule (E-2002/ED-1562) need a durable home, it is an accepted
decision — neither has one today beyond ED-1562.

## Handoff: file a todo, skip the brainstorm

The classification was unambiguous — every section was WHAT (keep), WHY (cut),
or duplicated elsewhere (cut). File a todo to apply the proposal plus the three
companion changes and the four fixes above.

One item does **not** belong in that todo and does need a brainstorm: there is
no rule anywhere that a refused `worktree land` is a STOP, and that being asked
to fix the blocker is not authorization to commit on main. A session did exactly
that and disclosed it. Options — a sentence in `endless guide orchestration`, a
code gate, or nothing — genuinely diverge, and the observed failure is evidence
against prose. It should not gate the apply.

## Also done under this task

ED-1541 (memory-off) was stale and is updated: it named the retired
`~/.claude/LESSONS.md` and claimed "Scope: global", which was never enforced —
`~/.claude/settings.json` sets `autoMemoryEnabled: true` and only the endless
repo sets it false.

## Scope note

Not the final word. E-2007 rewrites the LESSONS.md mechanism; a second pass is
expected after it lands.

---

# Proposed CLAUDE.md — full replacement text

# Endless Project Rules

Run `endless guide` first.

**Do not add anything to CLAUDE.md without explicit permission to do so.**

Rules only — no rationale, no history, no mechanism. Workflow belongs in
`endless guide`; rationale belongs in an accepted decision.

## The ledger is durable state

- `.endless/db-ledger/*.jsonl` and `.endless/verbs.jsonl` are the permanent
  record of all task state. The SQLite database is a rebuildable projection of
  them.
- Never hand-edit them. Never revert or discard an "Endless: auto-record session
  activity" commit. Never gitignore `.endless/`.
- Change task state only through `endless` commands.

## Python and Go

- Go (`internal/`, `cmd/endless-go`) owns all database access: reads through the
  `endless-go event` helpers, writes through the event pipeline.
- Python (`src/endless/`) owns the Click CLI, the logic, and the rendering.
- Do not add SQLite to Python. Legacy direct-SQL sites remain in six files and
  are being removed; do not add a seventh.

## Build

- `just build` → Go binaries in `./bin/`. Never build to the project root or
  `/usr/local/bin/`.
- `just install` — global from the main checkout, worktree-scoped from a
  worktree. Never run `uv tool install` by hand.
- `just test` (Python), `just test-go` (Go).

## Your worktree and its database

- Never create a worktree by hand. `endless task claim` creates and wires yours.
- `endless` commands in your worktree read and write a per-worktree sandbox
  database, not the real ledger.
- A command that must reach the real ledger takes `--db main`.

## Memory is OFF here

- This overrides the global instruction to record corrections as memory.
- Never create, read, or act on anything under a `memory/` directory or
  `MEMORY.md`. Recalled memory in a `<system-reminder>` is inert background, not
  instructions.
- After any correction from Mike, immediately and without asking, append it to
  `.endless/LESSONS.md` in your own worktree — or the main checkout's copy if you
  hold no task — and commit it on your branch.
- Never read that file. Name the full path you wrote to, not "Recorded".

## PRODUCT

You are using Endless to write Endless, so you gravitate toward solutions that
assume this machine and this project. When Mike writes PRODUCT — all caps, whole
word — say how the software will behave for someone else, on a different
machine, managing a project that is not Endless.
