# Evidence: worktree-pinned hook binaries

All measured 2026-08-13 during one incident that started as "a resumed session
is invisible" and ended as "session writes are broken machine-wide."

## How the pin works, and why

Each worktree's `.claude/settings.json` points every Claude hook at that
worktree's own `bin/endless-go`. That is E-998, "Add per-worktree
`.claude/settings.json` so spawned sessions test the worktree's hook" — landed
2026-05-02 (`564ce2b`), still `unverified`. Its rationale: the alternative was
repointing the global symlink, "which puts every other live Claude session on
the machine on the unverified build for the duration of the swap."

That reasoning holds for an ACTIVE worktree carrying candidate hook code. It
does not hold for a worktree whose branch has fallen behind, where there is no
candidate worth verifying and the pin only costs.

E-998 is not to be reopened — it shipped, and this changes its mechanism rather
than completing it. It stays as the record of why the pin exists.

## Failure 1 — the binary dies, and the session runs untracked

`session goto 1808 --resume` opened a live Claude session that endless could not
see: sibling panes could not resolve it, and its DB row still read
`state=ended`, `process=''`. Its binary was built Jul 29. Run read-only:

    resolve project: task_types integrity check: tasktype: id=1 slug mismatch:
      enum="task", table="todo"

That is E-1659's `task`→`todo` rename. The check aborts the process before it
does anything, so SessionStart never registers the pane — and the error goes to
hook stderr, which nobody reads. The session then runs untracked for its whole
life.

## Failure 2 — the same binary corrupts the SHARED ledger

Later the same day, every session began failing on:

    upsert session: SQL logic error: no such column: NEW.process

Chain, verified:

- `internal/schema/schema.go` EMBEDS `schema.sql` into every binary, and the
  pre-rename `schema.sql` (at `38aa359e^`) declares two triggers,
  `sessions_null_process_on_end_update` and `..._insert`.
- E-1898 renamed `sessions.process` to `process_id` and its change file drops
  both triggers before `ALTER TABLE sessions DROP COLUMN process`. That
  migration was CORRECT and is marked applied — SQLite refuses DROP COLUMN
  while a trigger references the column, so the column being gone proves the
  drops ran.
- A stale binary's schema-ensure then re-created both triggers via
  `CREATE TRIGGER IF NOT EXISTS`, against a table that no longer has the column
  they reference. Every session insert or state update failed from then on.

So a stale worktree binary is not merely self-harming. It writes damage into the
shared database that breaks every OTHER session on the machine. One dormant
worktree resumed after two weeks took down session writes for everyone.

## Scale, measured

    worktrees:                          135
    settings pinned to own binary:       89
    worktree binaries present:          103
    of those, containing pre-Aug-10 schema:  99   (96%)

Five of the 99 held LIVE sessions at the time — e-1901, e-1941, e-1919, e-1917,
e-1889 — so the corruption was ongoing, not latent. Remediated by copying main's
binary into all 99 and re-dropping the triggers; verified 103/103 current, 0
triggers, and session upserts landing again.

## Two measurement traps, both of which caught this session

**Timestamps are worthless.** e-1808's binary was timestamped 13:24 today and
contained Jul 29 code: `just build` had run WITHOUT the rebase, so the mtime
refreshed while the source did not. A freshness check based on mtime would have
passed that binary and delegated straight into the failure.

**The only reliable test is content.** `strings <binary> | grep -c process_id`
separated the 99 from the 4 in one pass. Whatever E-1972 chooses, the
compatibility signal must be derived from what the binary CONTAINS — an embedded
schema/enum fingerprint it can report — not from file metadata or branch
freshness heuristics.

**And it must run at hook time, on every event.** This session's own worktree
was compatible when created and went stale underneath a live session, with no
event marking the transition. A check performed once at worktree creation would
not have caught it.

## The loud-failure invariant, restated (Mike)

Inverting the hook binary is NOT a reversal of E-1662. The rule stands: when an
invariant fails, it fails loudly. What changes is which condition is the
invariant:

    If delegation determines the event should go to the worktree binary and that
    binary is absent or incompatible, THAT is the invariant violation and it
    fails loudly.

When delegation determines main's binary should handle the event, there is no
invariant to violate — main just handles it. So the inversion does not weaken
E-1662; it supplies the precondition it was always missing, because absence only
matters once something has decided the worktree binary was required.

Corollary: the delegation decision must be explicit and inspectable, since it is
now what determines whether an absent binary is an error or a non-event.

## The shape Mike proposed

Hooks always invoke main's known-good binary, which delegates to the worktree's
only when that one is verified compatible. Under the pin, the candidate binary
is the ONLY thing that runs, so when it breaks there is nothing alive to report
the fact — which is exactly why both failures above were silent. Inverting means
something working always starts.

Stated cost: the dispatch path in main's binary that runs BEFORE delegation is
never exercised as candidate code. Small and fixed; everything after the
delegation point is still candidate.

Open, and the reason this is a brainstorm rather than an implementation task:
what should drive the delegation decision. Mike: "delegation needs to be more
intelligent than opt-in vs. automatic."



---

## Absorbed: E-1704 and E-2038 (folded here 2026-08-23, both obsoleted)

Two tasks were obsoleted into this brainstorm rather than left to be invalidated
by whatever it decides. Their content is below so the correct task and plan can
be filed FROM this decision instead of rewritten around it.

### From E-1704 — bare shell invocations (was: self-re-exec into the worktree build)

From a bare (non-Claude) tmux pane inside a self-dev worktree, `endless` (Python)
and bare `endless-go` silently run MAIN's code. Worktree changes cannot be
exercised without `PYTHONPATH=src` or `./bin/endless-go`, and new worktree flags
error `No such option` until landed.

E-1704 proposed a gated, loop-guarded pre-parse self-re-exec INTO the worktree
build, fired on cwd matching the worktree path segment AND
`project_is_self_dev(project_root)`, with the worktree root taken as cwd
truncated at the match. Symbols it identified for reuse: Python
`config._WORKTREE_PATH_RE`, the Go equivalent in `cmd/endless-go/main.go`.

**Why it is obsoleted rather than kept:** its policy is the inverse of this
brainstorm's premise. E-1704 says prefer the worktree build; this task says
prefer main's known-good binary and delegate only when the worktree's is
verified compatible. The mechanism (predicate, loop guard, path truncation) is
reusable; the direction is not. The open question this brainstorm must answer
for the shell case: does a bare invocation delegate on the same compatibility
test as the hook case, or does it stay in main and warn?

### From E-2038 — the compatibility test itself

The `e-2011-home-relative-project-paths` change file ran against the shared main
DB at 2026-08-20T18:17:55, rewriting `projects.path` from absolute to `~/...`.
Worktree binaries built before that compare a raw absolute cwd against the new
format, miss every rung of `ProjectIDForPath`'s walk-up, and fall through to
`ensureAutoRegisteredProject`, which silently registers the worktree as a
project named after its directory.

Evidence: zero stray project rows before that timestamp, five after
(e-1944, e-1914, e-1975, e-1920, e-1733). All five store an ABSOLUTE path while
every legitimate row is home-relative, proving the writer predates E-2011.
114 of 156 worktrees held a binary older than the migration. This is the same
population this brainstorm measured on Aug 13 (103 of 135), and the same root
cause as the stale-binary hook abort — a worktree binary that cannot read
current state — reached by a different failure path.

**The proposed test, which is this brainstorm's "verified compatible":**
`_schema_version` names every applied change file, and a binary ships a known
set under `internal/schema/changes/`. Markers present in the DB that the binary
does not ship mean the DB is AHEAD of the binary. Refuse or decline to delegate.
Ahead-only — a binary shipping changes the DB lacks is every pre-land worktree
and must stay normal. Escape hatches (`db apply-change`, `db path`, `db backup`,
`rebuild-db`) must never be gated, or a refused binary cannot be recovered.

**Note the integrity-check abort this task already documents (a Jul 29 build
dying on the E-1659 `task`→`todo` check) is the same class:** stale binary,
current DB. A single compatibility gate should cover both, which is the argument
for deciding it here rather than in two tasks.

### Cleanup owed either way

Five stray project rows exist; `project unregister <name>`, ONE name per
invocation. `tasks`, `decisions`, `notes`, `activity`, `project_next` are
`ON DELETE CASCADE` from `projects` — measured at filing these rows held 0
tasks, 0 decisions, 0 sessions, 153 activity rows. Cleanup without the gate is
temporary: any stale worktree re-registers on its next hook event.

---

# Addendum — the land window (evidence from E-1969, 2026-08-25)

Filed as E-2061 before searching, which was the wrong move; obsoleted and folded
in here, where the area already has an owner. This is evidence and one
constraint, not a competing shape.

## What happened

E-1969 renamed `sessions.active_task_id` to `sessions.task_id`. Its land
succeeded, and immediately after "Landed E-1969 into main" a Claude hook logged
50 lines, one per worktree:

    endless-go hook: reap worktrees: .endless/worktrees/e-1203:
      query active sessions: SQL logic error: no such column: active_task_id (1)

Old binary, new database — the same disagreement this task exists to resolve,
reached from the other direction.

## Why the window exists

`worktree land` applies the branch's schema changes at Step 5.5, and the
Justfile refreshes the installed binary only AFTER `endless worktree land`
returns. Between those two moments the real DB is migrated and the installed
binary is not. `internal/hookcmd/claude.go` calls
`monitor.ReapWorktreesForProject` from five places; one of them fired inside
that window.

The order is not a mistake and cannot be fixed by reordering:

- E-1941 moved apply-change AFTER the ff-merge because applying before it left
  the DB migrated to a schema no installed binary understood — the 2026-08-10
  machine-wide freeze.
- It cannot move later: `_record_landing` runs the same binary against the real
  DB (E-1664, inverted).
- Refreshing the binary earlier only relocates the window; the new binary would
  then meet the un-migrated DB and fail on the same read.

## The constraint this puts on the chosen shape

"Hooks always invoke main's known-good binary, which delegates to the worktree's
only when that one is verified compatible" assumes main's binary is known-good.
For the seconds of a land it is not: main's IS the stale one. A compatibility
check derived from an embedded schema fingerprint — which this task already
requires, and rightly, over any mtime heuristic — will find BOTH binaries
incompatible during that window.

So the delegation decision needs a third answer beyond "use main's" and "fail
loudly because the worktree's is incompatible": what to do when neither binary
matches the DB. Two candidates, both consistent with the loud-failure invariant
(E-1662), and this is the open question rather than a recommendation:

- Treat mid-migration as a known, bounded state and skip rather than error —
  the hook paths already fail open, so a skip is a smaller change than it
  sounds, and the reaper's work is idempotent and retried on the next event.
- Treat it as loud but singular: report "DB is mid-migration" once, not once per
  worktree, so the operator sees one line instead of 50 and does not read a
  successful land as a failed one.

The read case is the benign half. The write case is not hypothetical — Failure 2
above is exactly it, a stale binary re-creating dropped triggers against
`sessions` and breaking session writes for every session on the machine. That
was E-1898's rename; E-1659's enum rename was the other. A rename is not a new
class of change here, it is the class that has already caused this twice.

## A comment to retract while you are here

`internal/schema/changes/e-1929-add-tasks-removed.go` states that the window
between schema application and the binary swap "is never entered in practice."
It is entered. That file is where later changes go to copy their ordering
reasoning from — E-1969's change file did exactly that — so the false claim
propagates until someone corrects it at the source.

# Addendum — a use-case for whatever shape is chosen (from E-2071, 2026-08-26)

Not a new failure mode, and not a request to widen this task. One constraint the
chosen shape should be checked against, recorded because it is easy to miss when
the framing is "which binary runs".

## The constraint

`cmd/endless-go/main.go` PinMainDB's `hook` and `tmux` UNCONDITIONALLY. An
explicit `--config-dir` still wins (E-1429), but absent that, hook traffic goes
to the MAIN database regardless of cwd or XDG_CONFIG_HOME — deliberately, since
a fired hook is real-world activity in the real record.

This is the mechanism behind "Failure 2" above, stated as a general property
rather than as a schema-trigger story: **worktree hook traffic is never
contained.** It is not that a stale binary happened to reach the shared ledger;
nothing a worktree's hook does is confined to that worktree's sandbox. The
binary-selection question and the database-routing question look independent and
are not — whichever binary wins, its hook writes land in main.

## Why it turned up

A session drove `endless-go hook claude` with a synthetic payload while testing
something unrelated. It expected the surrounding sandbox isolation to contain it.
It did not: a sessions row was written into the main database and bound to a live
tmux pane, and every companion-resolving command then refused that pane as
ambiguous.

## What to check the chosen shape against

- If main's binary delegates to a worktree's, the delegate inherits the main pin.
  A "verified compatible" worktree binary is still writing to the shared record,
  so compatibility has to cover WRITE shapes (schema, event kinds), not just
  "does it start".
- If a compatibility probe runs the candidate binary, the probe must not itself
  be a hook invocation, or the probe writes to main as a side effect of asking
  whether it is safe to write to main.
- The loud-failure invariant stated above should extend to this: a worktree
  binary about to write to the shared record on behalf of a hook is worth saying
  out loud, since the blast radius is every session on the machine rather than
  the one worktree.


---

# Addendum — the stray rows came back (evidence from E-1947, 2026-08-26)

The gate this brainstorm proposes is still undecided, and the section above
predicted what happens meanwhile: "Cleanup without the gate is temporary: any
stale worktree re-registers on its next hook event." It did.

## What recurred

The five rows named above (e-1944, e-1914, e-1975, e-1920, e-1733) are gone —
cleaned up. Two NEW ones have taken their place, both on 2026-08-26:

| id | name | path | created |
|---|---|---|---|
| 63 | e-1658 | `/Users/…/.endless/worktrees/e-1658` | 2026-08-26T05:58:47 |
| 64 | e-1947 | `/Users/…/.endless/worktrees/e-1947` | 2026-08-26T18:06:40 |

Both store the ABSOLUTE path while every legitimate row is home-relative, so
the fingerprint argument above identifies the writer as pre-E-2011 again. This
is the second cleanup cycle, not a leftover from the first.

## The e-1947 incident, end to end

Six activity rows, 18:06:40–18:07:35Z, all attributed to the new project 64.
The session had been dormant since 2026-08-17 and its worktree binary was that
vintage; `.claude/settings.json` pinned the hooks to it.

It stopped for a reason nobody chose. The session was recovering a failed land
and ran `git checkout -- .claude/settings.json` to clear the skip-worktree file
blocking a rebase. That restored main's tracked settings, which name main's
binary — so the hooks silently moved onto a current build mid-session and
attribution returned to project 1. Nothing announced either transition.

That is worth stating plainly: the symptom is not merely silent, it is
self-concealing. A window that opens and closes without a log line will not be
noticed by the operator, and the only durable trace is a project row nobody
reads.

## Both halves confirmed against the real code, not inferred

- Pre-E-2011 (merge-base 42674790): `ProjectIDForPath` queries
  `WHERE path = <absolute>` at each rung, and `ensureAutoRegisteredProject`
  writes `dir` absolute. Both reads match the stray rows exactly.
- Current build: given a worktree cwd with no project row (e-2016), run against
  a VACUUM copy of the real DB, it resolves to project 1 and writes nothing.
  The walk-up is correct today; only the stale binaries are wrong.

## Correction: there is no protective "loud abort" band — the checks do not run

An earlier draft of this addendum claimed stale binaries split into a loud band
(aborting on an enum integrity check) and a silent band. That was wrong, and the
way it was wrong is worth keeping.

Two pre-E-2011 binaries, pointed at a copy of the real DB with an explicit
`--config-dir`, aborted on the `session_task_relations` check before the lookup
ran. That looked like protection. It is a test artifact.

`monitor.DB()` wraps the schema apply AND all five enum integrity checks in
`if !pinnedToForeignRealDB()`. `hook` calls `PinMainDB()` whenever no explicit
`--config-dir` is given — which is every production hook invocation — and the
pin sets `dbPathOverride`, so `foreignRealDB` returns true on its first line and
the entire block is skipped. Passing `--config-dir` is what re-enabled the
checks and produced the abort. The gating is unchanged since the merge-base, so
this is true of the stale binaries too.

**On the one path that matters, no integrity check runs at all.** Every one of
the exposed worktrees registers silently on its next hook event; none of them
fail loudly first.

This constrains the shape: a compatibility gate placed where the current enum
checks live would inherit their gating and never fire for hooks — the exact
population it exists to catch. It has to run outside the pin test.

## Reproduced on demand, and a data-side fix that needs no rebuild

Production conditions simulated by pointing `$HOME` at a scratch copy (which
makes `realDBPath()` resolve there, so the pin targets the copy) and running
`hook prompt` with no `--config-dir`:

| copy | `projects.path` for id 1 | result |
|---|---|---|
| A | `~/Projects/endless` (current) | `auto-registered project: e-2016 …` — stray row, activity bound to it |
| B | `/Users/…/Projects/endless` (absolute) | no stray row, activity bound to project 1 |

The B row is the interesting one. `projectIDForResolvedPath` — the legacy-row
half of the current lookup — scans `projects`, resolves each stored path, and
returns the deepest row that is dir or an ancestor of it. It was written to see
through every stored spelling at once, so a row stored ABSOLUTE is still found
by a current binary; verified separately, current build against copy B resolves
a worktree cwd to project 1 and writes nothing.

So storing id 1's path in the absolute form is understood by BOTH generations:
old binaries match it directly on the indexed walk, current binaries reach it
through the legacy scan. It is one UPDATE, no patch, no rebuild, and no worktree
touched.

What it costs, stated so it is a decision and not a trick:

- It gives back E-2011's legibility for one row — `endless project list` shows
  one absolute path among 53 home-relative ones.
- Every hook event for that project now takes the scan fallback instead of the
  index. The table is 54 rows.
- Nothing rewrites it back: `RepairProjectPaths` is called only from the
  e-2002 and e-2011 change files, both already applied and marked.
- It must be paired with deleting the stray rows first, or an old binary's walk
  matches the stray row before it reaches the project row.
- Nothing in the tree assumes the stored form begins with `~` (checked).

This is mitigation, not the fix. It closes the stray-row failure path for every
stale binary at once while the gate is still being decided; it does nothing for
the other stale-binary failures this brainstorm covers, and it should be undone
by re-running `RepairProjectPaths` once the gate exists.

## Population today (compare 103/135 on Aug 13, 114/156 at filing)

- 135 worktrees; 106 hold a `bin/endless-go`; 96 pin their hooks to it.
- 67 of those binaries lack E-2011's code.
- **57 worktrees both pin to their own binary AND carry a pre-E-2011 build** —
  the actually-exposed population, each one a stray row waiting for its next
  session.

Discriminator used, if it is wanted again:

```sh
strings <wt>/bin/endless-go | grep -q "storing project path" || echo stale
```

## One constraint this adds for the shape

A fix distributed as a COMMIT cannot reach these worktrees, and it is worth
recording why before someone tries:

- `bin/` is gitignored, so no commit can carry a binary.
- `.claude/settings.json` — the file that decides which binary runs — is tracked
  but carries `git update-index --skip-worktree` in every worktree (E-998), so
  git deliberately will not update it there. That is not a detail: it is how the
  e-1947 land failed in the first place.

Whatever lands must therefore be either a gate inside the binary that runs (this
brainstorm's shape) or a sweep executed over the worktrees. It cannot be a
change that merely rides into their branches.



---

# Addendum — the write case, observed (2026-08-26, from E-1947)

"Failure 2 — the same binary corrupts the SHARED ledger" above was argued from a
prior incident. Here is another, produced accidentally and therefore with a
known cause and a measured blast radius.

## What happened

Remediating the stray-project rows, 35 worktrees had their
`internal/monitor/db.go` patched for the E-2011 path format and were rebuilt
with `go build`. That replaced each `bin/endless-go` — which had been a COPY of
main's binary taken at claim time — with a build of that worktree's own, older
source. Roughly twenty minutes later every Claude hook on the machine began
failing:

```
claude: touching session: upsert session: SQL logic error: no such column: NEW.process (1)
```

Two triggers, `sessions_null_process_on_end_{update,insert}`, had reappeared in
the REAL database. They reference `sessions.process`; the column is `process_id`.
E-1898 removed both deliberately and its change file drops them.

## The mechanism, and why it is the write case

An old binary carries its source's `schema.sql`, and `monitor.DB()` executes it
on connect. Every statement in it is `CREATE ... IF NOT EXISTS`, so it can never
drop or alter anything — it can only RESURRECT objects the current schema has
since removed. A rebuilt worktree binary is therefore not merely a stale reader:
it is a writer that re-creates deleted schema in shared state.

This is worse than the read case in one specific way. The read case fails on the
binary's own query. This one lands in the database and breaks EVERY process on
the machine, including current ones — main's binary could not write a session
row either, because the damage is in the data, not the code.

## Blast radius, measured

- Diffing the live DB against the previous night's backup: 90 objects on both
  sides, exactly two appeared, nothing else changed.
- Diffing what those old `schema.sql` files create against main's: six objects
  differ, and four (`channels`, `conversations`, `messages`, `session_kinds`)
  already exist, so `IF NOT EXISTS` skips them. Only the two triggers can appear.

So the exposure today is small and bounded. Two properties of it are not:

1. The set is the DELTA between main's schema and each old vintage. It widens
   every time main removes a schema object, silently, with no signal at the
   moment of widening.
2. This failure was loud only by luck — the resurrected trigger happened to name
   a renamed column. A resurrected trigger whose columns all still exist would
   have run, and written, and said nothing.

## What it argues for the gate

The `_schema_version` test proposed above answers this case as well as the read
case, and it is the same one call: markers in the DB that the binary does not
ship mean the DB is AHEAD, so refuse. Critically, the refusal has to come BEFORE
`monitor.DB()` executes `schema.SQL` — a gate that runs after the connect is
open has already let the old schema land.

Note also that the pin is what hid this. `hook` calls `PinMainDB`, and
`monitor.DB()` skips the schema apply AND all five enum integrity checks when
`pinnedToForeignRealDB()` is true — so the hook path, the busiest path, applies
no schema and verifies nothing. Whatever invocation applied the old schema was
therefore NOT a hook; it reached the real DB by another verb. That verb was not
identified, which is its own argument for a gate rather than an audit.

## Operational note

`sessions` writes are restored by re-running E-1898's two DROP statements. That
is a repair, not a fix: any of the 35 can re-apply on its next run. The repair is
scripted (`fix-endless-triggers.sh`: back up, drop, then PROVE a `sessions`
write succeeds, then report any trigger the schema tree does not create). It
found one pre-existing orphan, `focuses_updated_at`, which predates this
incident and was left alone.
