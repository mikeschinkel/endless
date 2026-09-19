# Plan — the mirror follows the ledger to main, then moves in beside its task

Two phases, in this order. Phase A redirects mirror writes to main and drains the
backlog off branches; phase B consolidates the files under `.endless/tasks/e-NNNN/`.
They were E-2137 and E-2138 until 2026-09-15, when Mike merged them because both
would be reviewed and landed in one sitting, which removes the only reason to
isolate them — phase B's 712-file rename is not risk the phase A work should wait
behind if they ship together anyway.

The ordering below is NOT optional and is the reason the two were sequenced in the
first place. It is now a phase boundary rather than a task boundary; nothing else
about it changes.

## The decision

A task's multiline content is written twice: the **ledger entry**, which is
enforced to land on the main checkout only, and the **mirror**
(`.endless/{plans,analyses,outcomes,decisions}/<ID>.md`), which today goes to the
task BRANCH whenever a worktree exists and waits for a land to reach main.

Both halves become main-bound. The mirror is a projection of a database column;
it follows the column's own commit to the same place, at the same time.

Nobody decided the split. E-1525 found the file UNTRACKED and committed it the
nearest way to hand; the goal was "get it into git", not "get it onto the
branch".

## Why this is the cause and not a symptom

Measured 2026-09-14 with the content-based probe that drives the diamond marker
and reclaimability (`rev-list` counts by SHA and says 890; the real number is
139):

| | |
|---|---|
| worktrees unlanded by content | 56 of 133 |
| unlanded commits they hold | 139 |
| of those, doc-mirror commits | 123 |
| of those, real work | 9 |
| worktrees unlanded ONLY because of doc mirrors | 44 |

E-1881 proposed a background job to sweep those commits up to main forever.
This stops creating them. Under ED-1550 that makes this the cause.

## Scope, decided

**Every write site routes to main.** `_mirror_doc_to_worktree` (task_cmd.py) and
the decision mirror (decision_cmd.py) both call the existing main path —
`endless-go event commit-doc` → `events.CommitDoc` → `commitPaths`, which already
enforces main-checkout-only, strips the GIT_DIR family, and amends a repeat of
the same subject rather than piling up. `_commit_doc_in_worktree` is deleted.

**No worktree materialization at all.** `_materialize_task_docs` and
`_materialize_task_doc` stop writing into the worktree. Nothing in Endless reads
that copy, and the raw read path for an agent is verified to work from inside a
worktree:

```
endless-go --config-dir <cfg> session-query task-field --id <N> --name text
```

The tempting middle option — write the worktree copy but leave it uncommitted —
is what E-1525 removed: it makes every worktree permanently dirty, trips land's
modified-worktree guard, and pushes another hundred worktrees into reading as
modified.

A worktree copy also invites an agent to hand-edit content the database owns,
the same failure `.endless/LESSONS.md` needed a CLAUDE.md rule to prevent. A file
that is not there cannot be hand-edited.

**Decisions stop following cwd.** `decision_cmd` writes to
`worktree_root_for_cwd()` today, so a project-wide artifact lands on whichever
task branch you happen to be standing in. It becomes unconditionally main-bound.
(No harm from this was measured — the three worktrees holding ED-1541 share one
commit through orphaned ancestry, not through this path — but it is wrong by
construction.)

**`commitPaths` gains a retry.** There is no `index.lock` handling anywhere in
`internal/events`; concurrent writers fail hard. `land` carries
`LAND_MAX_RETRIES = 8` for exactly this, so the pattern exists to copy. This
change adds writers to that path, so it carries the fix.

**A failed write warns and is repaired later.** The DB write is authoritative and
has already happened; failing the command would report failure for work that
partly succeeded, which E-1474 rejected for land. So: warn, continue, and a
reconcile pass compares each mirror on main against its column and rewrites the
file when they differ. Safe unconditionally — the file is derived, so
regenerating it can never lose anything. It fits the job runner E-2128 built.

## Draining the backlog

123 doc-mirror commits sit on branches now. They are drained here rather than
left to age out, because phase B cannot safely run until they are gone (below).

Per branch, for each doc file it touched:

1. **Compare the branch's content against the DB column.** The DB is the source
   of truth. Equal — measured as 125 of 134 such files — means nothing is at
   stake. Different means the branch holds content the database does not:
   report it with the command to adopt it
   (`endless task update E-N --text-file ...`) and touch nothing. Never guess
   which side wins; that is E-1500's existing question and its existing answer.
2. **Write the DB content to main**, through the same path as a normal write.
   This is a write, not a merge, so it cannot conflict.
3. **Drop the doc commits from the branch**, under a compare-and-swap on the
   branch ref.

Step 3 is what actually settles a branch. Steps 1-2 make the content safe; only
removing the commits stops the branch holding what main lacks.

### Why this does NOT skip worktrees that are in use

`worktree sync` skips them, and copying that rule here was wrong. Its reason does
not transfer:

- `sync` REBASES ONTO A NEWER MAIN, which rewrites every file under a running
  agent. That is the E-2090 hazard its gate exists for.
- This drops specific commits WITHOUT MOVING THE BASE. The only files whose
  content changes are `.endless/{plans,analyses,outcomes}/*.md` — which, after
  this task, nothing reads and no worktree materializes. Every other file in the
  working tree is byte-identical before and after.

Skipping in-use worktrees would therefore leave doc commits on exactly the
branches most likely to be long-lived, for no safety gained — and those commits
are what E-2138 cannot tolerate.

The real hazard is narrower: a session COMMITTING while the rewrite is in
flight would have its commit stranded on the old tip. That is solved where such
races are always solved, with a compare-and-swap:

    git update-ref refs/heads/task/<N> <new-tip> <old-tip>

The update fails if the ref moved since it was read, so the drain retries or
leaves that branch alone. It can never clobber a commit it did not see.

One condition still forces a skip: a worktree whose git state is MID-OPERATION —
its own rebase, merge or cherry-pick in progress. The ref is not ours to move
then. `_git_state_anomaly` already detects exactly this for `worktree sync`.

Do not reintroduce a liveness gate here. It was considered and rejected on the
grounds above; if a future change makes the drain move the base, that reasoning
lapses and the gate comes back with it.

## Why phase B cannot run before phase A

TESTED, not assumed, on a scratch repository:

- A branch that MODIFIES a file main renamed rebases cleanly. Git follows the
  rename and applies the edit at the new path.
- A branch that ADDS a file at the old path does NOT. Git's directory-rename
  detection saw `.endless/plans/` become `.endless/tasks/e-0/` and placed the
  branch's new `E-2.md` at `.endless/tasks/e-0/E-2.md` — one task's content
  inside another task's directory. It raised a conflict rather than doing it
  silently, but that is the resolution it offers.

`.endless/plans/` does not rename to one directory; it fans out to 432, and git
picks whichever it detected. "Endless: add plan for E-N" is the most common
commit shape on these branches, so this is the common case, not the edge.

Draining first removes the input entirely. That is why phase A comes first, and
why the drain is not optional even though the mirrors it removes are derived.

## What retires with this

- `_commit_doc_in_worktree` and `_commit_plan_file_in_worktree`.
- `_reconcile_orphan_plan`'s branch-copy comparison (E-1500). It reads
  `git show <branch>:<plan>` against `tasks.text` and asks the operator which to
  keep. That mismatch can only exist BECAUSE the plan is on the branch; once it
  is not, the code stops having a job. Re-examine it here rather than leaving it
  to rot.
- E-1881's step 1, entirely.
- `.endless/plans/`, `.endless/analyses/` and `.endless/outcomes/` as directories,
  once phase B has emptied them.

## Prior decisions checked

**ED-1169** rejected "(a) auto-commit silently, (b) commit on the new task
branch" because "auto-commit hides intent". It does not block this: it governs a
plan file a HUMAN left uncommitted in main at claim time. A mirror Endless
authored is not anyone's hidden intent, and `events.CommitDoc` already
auto-commits exactly these files to main whenever no worktree exists. This makes
the existing behaviour the only behaviour.

**ED-1580** is REJECTED and must not be cited here. Its rejection reason is a
caution worth heeding: "over-generalized from one measurement... bundles three
claims."

## Verification

- A task update from inside a worktree writes the mirror on main and leaves the
  worktree untouched — no new file, no new commit, `git status` unchanged.
- The same from outside a worktree is unchanged from today.
- A decision written from inside a worktree lands on main, not on that
  worktree's branch.
- Worktree creation materializes no doc mirrors, and the raw read path returns
  the same content the DB holds.
- `commitPaths` retries a held `index.lock` and succeeds rather than failing.
- A mirror deliberately corrupted on main is rewritten by the reconcile pass to
  match its column, and a mirror matching its column is not rewritten.
- Drain: a fixture branch whose doc content EQUALS the DB is drained and the
  branch no longer holds doc commits; one whose content DIFFERS is reported and
  left untouched; one whose worktree is in use is skipped and reported.
- After a drain, the fleet's content-based unlanded count falls and no worktree
  reports content the DB does not have.
- `just test`, `just test-go`.




# Phase B — consolidate task content under `.endless/tasks/e-NNNN/`

Absorbed from E-2138, which is declined in favour of this task.

## The move

`.endless/tasks/e-NNNN/` already exists — 238 entries today, each holding that
task's `verify.sh` beside the shared `_harness.sh`, `_guard.sh` and `CLAUDE.md`.
This consolidates the rest of a task's content into the directory that already
belongs to it rather than inventing a layout:

```
.endless/tasks/e-2128/plan.md       <- .endless/plans/E-2128.md      (432 files)
.endless/tasks/e-2128/analysis.md   <- .endless/analyses/E-2128.md   (146 files)
.endless/tasks/e-2128/outcome.md    <- .endless/outcomes/E-2128.md   (134 files)
.endless/tasks/e-2128/verify.sh     (already there)
```

**Decisions do not move — and that exclusion is deliberate, not an oversight.**
`ED-NNNN` has no owning task today, so it is not task-scoped;
`.endless/decisions/ED-NNNN.md` stays where it is.

Do NOT "complete" this by moving them as well. E-1868 merges decisions onto
tasks and renumbers every decision id in the process, so moving the files now
means moving each one twice and landing it at a name that is about to be wrong.
That epic owns the relocation, at the new id, and the obligation is recorded on
E-1861. Same reasoning Mike applied to leaving `decisions.text` alone for
E-1000.

## Why this is worth doing beyond tidiness

**It fixes an allowlist that has already gone stale once.** E-1881's step 1 must
name the directories holding task content. Its description, written 2026-08-04,
named `plans`, `analyses` and `db-ledger` — and by 2026-09-14 the real set was
plans, analyses, outcomes and decisions, while `db-ledger` had become wrong
outright (those commits are orphans to drop, not content to push up). Every
future content type repeats that failure.

Afterwards the allowlist is one glob — `.endless/tasks/e-*/*.md` — that no new
content type can invalidate. This matters more now that E-1531 will add content
kinds on an ongoing basis rather than once.

**It fixes a casing split.** `E-2128.md` sits inside `e-2128/` today.
`.endless/tasks/CLAUDE.md` already warns that a hand-written path "gets that
wrong silently on a case-insensitive filesystem and loudly on everyone else's".

## The two-lifecycle question, and why it is not split-brain

After phase A, a worktree's `.endless/tasks/e-NNNN/` holds only `verify.sh` —
branch-authored, lands with the task — while main's holds that plus the `.md`
files Endless wrote there directly.

That is one directory fuller on main than on a branch, which is exactly what
`.endless/db-ledger/` already is. It is only split-brain in the other order,
where a stale `plan.md` sits beside a live `verify.sh` with nothing telling a
reader which is authoritative.

`.endless/tasks/CLAUDE.md` must be updated to say which files in that directory
are the task's to write and which are Endless's.

## Migration collisions

A rename on main is a modify/delete conflict for any branch still holding a
commit at an old path. Measured 2026-09-14: 51 worktrees held 316 doc-mirror
commits at old paths. Phase A's drain removes that input, which is the whole
point of the ordering — what remains after the drain should be small, and
almost all of it is the add/add shape where both sides are byte-identical
(125 of 134 such files matched).

## Phase B verification

- Every `.endless/plans|analyses|outcomes/*.md` has a counterpart at
  `.endless/tasks/e-*/<type>.md` with identical bytes, and the source
  directories are empty.
- `task show` renders plan, analysis and outcome from the new paths.
- A task whose directory did not exist gets one created.
- The casing is `e-NNNN` throughout, with no `E-NNNN` path component remaining.
- A worktree created after the move materializes no `.md` files, per phase A.
- `just test`, `just test-go`.




# Addendum — 2026-09-15, agreed with Mike before implementation

Two changes to the plan above. Both were settled by asking rather than guessing;
neither changes what the task is for.

## The reconcile job also RELOCATES, so phase B converges instead of migrating

The plan above gives the background job one job: compare each mirror on main
against its column and rewrite the file when they differ. Phase B's move to
`.endless/tasks/e-NNNN/` is then a one-time rename of 712 files.

The job does BOTH. On each pass, per project:

1. Relocate every `.endless/{plans,analyses,outcomes}/E-NNNN.md` it finds on
   main to `.endless/tasks/e-nnnn/<type>.md`.
2. Rewrite any mirror whose bytes differ from its column.

The reason is the straggler, and it is not hypothetical. A worktree created
before this lands is running its own older copy of Endless, keeps writing
mirrors to the old path on its own branch, and delivers them to main whenever it
lands — which may be weeks from now. A one-time rename cannot see those; it
would have to be re-run by hand, forever, by someone who remembers. A convergent
sweep collects each one on its next tick.

Consequences:

- **No mass rename in this branch's diff.** The branch carries code and tests.
  The layout arrives on main because the job puts it there, the same way every
  other mirror write now reaches main.
- **No one-time migration command.** There is nothing to remember to run, and
  nothing for a downstream project adopting this version to run either: their
  first monitor tick converges their tree.
- Relocation is a git-tracked move, committed on main like any other mirror
  write, so history follows the file.

## The branch-history cleanup covers decision mirrors too

The plan's drain names the three task-doc kinds. It also strips commits holding
`.endless/decisions/ED-NNNN.md`.

Same accident, same safety argument: `decision add` writes into whichever task
worktree you happened to be standing in, the DB row is the source of truth, and
the file is derived from it. Excluding them would leave those branches reading
as holding unlanded work after the cleanup — which is the number this task
exists to move.

## Naming

- `endless worktree strip-docs [--apply]` — the one-time branch-history
  cleanup ("drain" above). Dry run by default, like `worktree sync`.
- Job name `doc-mirrors` — the convergent main-side sweep.
