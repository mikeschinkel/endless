Ledger auto-commits on main are amended in place as new events append, so the
tip commit's SHA keeps changing. A worktree branch forked from one of those
SHAs is orphaned from its base as soon as the next event lands: main holds
equivalent (superset) content under a different SHA, and the branch still
points at the superseded one. On land, the rebase replays the orphan and
conflicts on the append-only ledger segment.

`_drop_orphan_amendable_commits` (E-1342) already handles this for a
contiguous run of orphans at the branch base. Its docstring puts mid-branch
orphans out of scope deliberately: a non-amendable commit followed by an
amendable one only arose from pre-E-1309 contamination, and dropping a
mid-branch commit risks deleting intended work. The open question is what to do
when that assumption no longer holds.

Observed on the e-801 worktree. Its branch reflog showed `Created from main`
with no commits of its own before the session's, yet `git cherry main HEAD`
reported all but three of its commits as patch-equivalent to commits on main —
the inherited run of amended ledger commits. The land reported dropping base
auto-amend commits and then conflicted anyway, so the existing helper did not
cover this shape. Recovery-and-retry could not converge because each replayed
ledger commit conflicts in turn.

Two framings, and the choice between them is the point of this brainstorm.

Treat it as a rebase problem, and the levers are: widen the E-1342 helper past
contiguous-at-base once it can distinguish an inherited orphan from real work;
detect the condition before rebasing and refuse with a named diagnosis rather
than discovering it mid-conflict; give the ledger segments a union merge driver
so concurrent appends stop conflicting; or reset a branch onto main when its
base is known-orphaned and only its own commits need replaying.

Treat it as a commit-shape problem, and the lever is upstream of all of that:
amending a commit that other refs may be built on is what manufactures the
orphans. Appending a new ledger commit instead of amending would cost more
commits on main but leave every fork point stable. Whether the amend behavior
earns its keep, and what it was buying, needs establishing before the rebase-side
options are worth ranking.

Two gaps are evidence rather than separate tasks, since their fix depends on
which framing wins:

- `worktree land --dry-run` does not simulate the rebase, so it cannot predict
  the failure it exists to preview.
- `worktree drop` documents a refusal for unlanded worktrees that its
  implementation does not perform; it checks foreign state and uncommitted
  changes only. Revisit after E-1898 and E-1941 land.

Relates to E-1941, a different failure from the same root condition: `just
land` applies schema changes with a binary rebuilt from still-stale worktree
source, because the rebase onto main runs afterward.

A correction worth keeping, because it cost this session three wrong
conclusions: commit counts mislead here. A branch can read as hundreds of
commits ahead while being an ancestor of main in content. `git cherry` and a
tree comparison tell the truth; `git reflog show <branch>` tells you where the
branch actually came from, which is what finally identified the cause.

## Root cause identified: `pull.rebase=true` blinds canAmend's guard (E-1941 session, 2026-08-11)

The orphan is not produced by amending per se. `canAmend`
(internal/events/commit.go:205-223) already refuses to amend a tip another ref
can reach — precondition 2, "HEAD must not be reachable from any ref BESIDES the
current branch." A worktree branch forked from main's ledger tip normally
triggers that refusal and endless appends instead. The commit-shape lever this
brainstorm proposes is therefore already implemented; the question is why it did
not fire.

Because it implements reachability as a SHA test: `git for-each-ref --contains
HEAD`. `git pull --rebase` rewrites every local main commit to a new SHA. The
task branch still points into the old, now-unreachable SHAs, so it no longer
"contains" main's HEAD. The guard is not bypassed and does not error — it
answers a reachability question the rewrite made meaningless, returns "safe to
amend," and the next amend diverges main from every task branch.

Reproduced with a harness that re-implements precondition 2 faithfully (the
`for-each-ref --contains HEAD` test, skipping the current branch) and reports
its decision either side of a pull:

    G1  no pull:          guard -> APPEND (saw refs/heads/task/900)   land: OK
    G2  after git pull:   guard -> AMEND  (saw no other ref)          land: CONFLICT
                                          conflicts: .endless/db-ledger/seg.jsonl

Mike set `pull.rebase true` manually at repo scope on 2026-08-10. main's reflog
carries the resulting rewrite: `e3ef6acb main@{2026-08-10 05:07:46}: rebase
(finish): refs/heads/main onto 43f238a9`, ~330 commits, about 3.5 hours before
E-1941 was filed. `pull.rebase=false` preserves local SHAs (it adds a merge
commit), so the guard keeps working — the merge strategy is an unstated,
load-bearing precondition of a safety check, not a style preference. Mike's ask
is that endless support either setting.

Consequences for the levers listed above:

- **The union merge driver for ledger segments must be struck.** It would
  corrupt the ledger. `Event` (internal/events/event.go:17-26) carries no unique
  id, and neither `ReadAllEvents` nor the projector dedups; union merge's defining
  behavior is to keep both sides' lines in a conflicted region, manufacturing
  duplicate entries that replay double-applies. `verbs.jsonl` tolerates union only
  because `matchers._add_verb_to_file` dedups at load. It also contradicts
  standing policy: land Step 3.75 refuses branch-side ledger commits and E-1342
  drops orphaned ones, so a merge driver would legitimize the cross-branch ledger
  merging the rest of the system refuses. (`ReadAllEvents` does re-sort by kairos
  TS, so the corruption vector is duplication rather than ordering — that does not
  rescue the idea.)
- **Any fix that stays SHA-reachability-based inherits the bug.** Widening the
  E-1342 helper, or refusing early with a named diagnosis, both still ask "is this
  commit reachable from another ref," which a rewrite answers wrongly. A durable
  fix needs an identity for a fork point that survives a history rewrite, or it
  needs to stop the rewrite from reaching ledger commits.
- **The framing choice may be moot.** This is not "rebase problem vs commit-shape
  problem" — the commit-shape guard exists and is correct; an external history
  rewrite defeats it. The live question is how endless behaves when main's history
  is rewritten under it, under either `pull.rebase` setting.

Also tested and negative, for whoever prices the E-1941 plan: rehearsing the land
on a throwaway branch off main and *merging* the task branch into it conflicts on
the same file, in the same cases, as the current rebase does. Four scenarios were
run (amend alone; pull --rebase alone; both; both with a union driver) against
two strategies — rebase the task branch onto main, versus branch off main and
merge the task branch in — and the two strategies gave an identical verdict in
every one. Changing the merge shape does not help a guard that has gone blind.

## Arguments tried and discarded (E-1941 session, 2026-08-11)

Recorded so this ground is not re-covered. Each of these was argued, tested, and
failed; the first two were mine and wrong.

1. *"The amend causes the conflict; `pull.rebase` does not."* Rested on a harness
   that called `git commit --amend` directly. That bypasses canAmend, so it
   tested a scenario unreachable in real endless — G1 above shows the guard
   refuses exactly that amend. Attributing the cause to the mechanism instead of
   to what disables the mechanism inverted the diagnosis. The pull is the cause.

2. *"Give the ledger segments a `merge=union` driver."* Listed as a lever in this
   analysis and briefly recommended as the fix. It would corrupt the ledger: no
   event identity, no dedup on the read path, so union's duplicate lines
   double-apply on replay. Struck above with the full reasoning.

3. *"Merge instead of rebase, so the orphan is never replayed."* The most
   plausible reading of the E-1941 plan's "branch off main, merge/rebase the task
   branch onto it," and the strongest remaining case for the throwaway rehearsal
   branch. Tested and false — merge conflicts identically, because the merge-base
   still sits behind the amend, so both sides have touched the append region.

4. *"Endless never runs `git pull` itself, so no code path branches on the
   setting."* True and irrelevant. The user runs `git pull`. A safety guard that
   silently degrades under a standard git setting has the defect regardless of
   who types the command.

What survived: the current land already has the rehearse-then-pointer-move shape
(Step 4 rebases in the worktree with a clean abort; Step 5 is `merge --ff-only`,
a pure pointer move), so E-1941's DB-ahead invariant needs only the apply
reorder, not a new merge mechanism.
