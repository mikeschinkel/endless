# E-1972 — How Claude hooks choose between main's and a worktree's endless-go

Brainstormed with Mike 2026-09-20. This decides **binary selection** only (the
axis E-2048 named and assigned here). DB target and artifact root are other
tasks' business.

---

## 1. What was already settled before this brainstorm started

Most of what this task's own analysis presents as open was closed by accepted
decisions while the analysis was being written. Re-establishing this first,
because it shrinks the task substantially.

- **What "compatible" means — closed by ED-1570.** A binary works with exactly
  one schema version. Same version, fine; any difference, stop. There is no
  compatibility range to compute or maintain. The phrase "verified compatible"
  in this task's description has no gradations left in it.
- **Whether a safe policy guts the point of per-worktree binaries — closed by
  ED-1567,** which accepted the cost in writing: a self_dev branch carrying an
  unlanded migration has its candidate hook degraded for the whole pre-land
  window, reducing what E-998 exercises on exactly those branches. That trade
  is already paid.
- **Who applies migrations during a land — closed by ED-1571, built by E-2088**
  (landed 2026-09-16, `901e589`): a migration-only executable compiled from the
  landing branch.
- **PRODUCT — closed by ED-1571.** Every project that is not Endless-developing-
  Endless has one installed binary and no land at all. There is no second binary
  to choose between, nothing to declare, and none of this mechanism should exist
  there. Binary selection is a self_dev-only concern.

## 2. The decision

**Hooks always invoke main's binary. It either handles the event itself or
spawns the worktree's binary, and it does so only on an explicit per-task
declaration.**

Filed as ED-1595.

### 2a. Declaration, not inference

The alternative considered and rejected was deriving the choice from the branch —
"does this branch modify hook-path code relative to main." Rejected because it
guesses intent from a file list: the list has to be written down, kept current
as code moves, and is wrong at the edges (a change to a shared helper the hooks
call; a change that alters hook behavior only through config).

Mike's argument, which is the load-bearing one: the use-cases cannot be
enumerated in advance. Nobody can write the rule that covers cases they cannot
envision. But the person doing a task knows whether they are working on hook
code. Declaring beats inferring.

This also satisfies a requirement the analysis already states and a heuristic
cannot: *"the delegation decision must be explicit and inspectable, since it is
now what determines whether an absent binary is an error or a non-event."*

### 2b. The default is main's binary — deliberately opt-in

**Recorded honestly: this is opt-in, and an earlier draft of this synthesis
oversold it as something more.** That draft argued the declaration was better
than a toggle because it gets verified at hook time. Mike pushed back, correctly:
the verification is a separate layer that exists under *any* policy, so it is not
a property of the declaration and cannot be used to dress it up.

Opt-in is nevertheless right here, for a reason that had not been stated: **the
two ways of being wrong cost wildly different amounts.**

- *Declared main when the worktree was wanted:* hook code goes untested until it
  lands; a bug surfaces later. Recoverable.
- *Declared the worktree when main was wanted:* an unvetted build runs on every
  hook event. Observed consequences, twice: sessions running untracked, stray
  auto-registered project rows, and resurrected schema objects that broke
  session writes for every process on the machine.

When one error is "a bug ships late" and the other is "the machine stops
working," the expensive one must require a deliberate act. That is what opt-in
is. It is not a weak compromise; it is what the asymmetry demands.

The cost of defaulting to main is also lower than it was a month ago: ED-1567
already degraded candidate hooks on every migration-carrying branch, so the
amount of testing E-998 was buying is already reduced by accepted decision.

### 2c. Amendment to this task's original framing

The seed framing was *"delegation needs to be more intelligent than opt-in vs.
automatic."* Amended by Mike this session: **the intelligence does not belong in
the delegation policy. It belongs one layer down, in the version verification.
The policy itself should be dumb, and dumb in the safe direction.**

### 2d. Who declares, and when

**Option 1 with Option 2 available**, in the terms used in the conversation:

- Spawn asks nothing. There is no prompt at spawn time — that is the moment with
  the least information, and a forced choice there would be answered by whatever
  dismisses the prompt fastest.
- **The session declares early**, once it has read its task and seen what it is
  touching. This is the normal path.
- **Mike may declare at any time,** before or during the work.
- Therefore it must be settable at any point through a command, not a
  hand-edited file. Changing it mid-task is a first-class case: discovering that
  you are now touching hook code is common.

Known risk, accepted: sessions may not bother, and hook exercise decays by
neglect rather than by decision. Mitigated by 2f.

### 2e. Stored worktree-locally, not in the database

Three reasons, in order of weight:

1. **It must be readable before the database is open.** The gate runs *before*
   `monitor.DB()` connects, because connecting is what let an old binary write
   damaged schema into the shared ledger. A flag in `tasks` would require
   opening the database to learn whether to delegate.
2. **PRODUCT.** A column on `tasks` that is NULL for every project that is not
   Endless-developing-Endless is self-dev leaking into the shared schema — a
   field every other user's database carries and never fills. A worktree-local
   file simply does not exist on those projects.
3. **Its lifetime is right.** The declaration is meaningful only while the
   worktree exists, and a file in the worktree dies with it. A row outlives the
   thing it describes.

The database's advantage is visibility. Get that by having `task show` *read*
the worktree file, not by storing the value twice — two sources that can
disagree is bad generally, and this pair can disagree in the one direction that
matters (database says delegate, worktree says do not).

### 2f. The declaration and the announcement are the same act

The worst incident in the evidence was invisible in both directions: a session
ran its own stale binary, then a routine `git checkout` restored tracked
settings and silently moved it onto a current binary mid-session. Neither the
breakage nor the recovery printed anything; the only trace was a row nobody
reads. Hook stderr is proven useless.

So the session states which binary it is on in its opening message. This costs
nothing extra — the session is already making the declaration — and it is also
what keeps 2d's decay risk visible.

## 3. Correction — this task's analysis contains a superseded proposal

**Do not implement the ahead-only rule.** The material folded in from E-2038 on
2026-08-23 proposes: markers present in the DB that the binary does not ship
mean the DB is ahead, so refuse; and *"a binary shipping changes the DB lacks is
every pre-land worktree and must stay normal."*

ED-1570 was accepted two days earlier and ED-1567 says the opposite: a
**candidate** binary with the DB behind it **refuses**, because it may never
migrate the real ledger. A session implementing the analysis top-to-bottom would
land a direct violation of an accepted decision.

The rule is ED-1570's: exact version agreement, in both directions, for a
candidate binary.

## 4. Recommendations carried into implementation, not decided here

Mike's instruction: write these up, overridable later, not blocking.

- **How main's binary learns the worktree binary's version.** Preferred: an
  inert command on the worktree binary (a `schema-version`-shaped verb) that
  prints a version and touches no database. Its useful property is
  self-bootstrapping — a binary old enough to predate the verb fails the check
  by not answering, so no table of old vintages is needed. The alternative is
  scanning the binary file for a marker string without executing it: works on
  every vintage ever built, but breaks quietly if the marker's spelling changes.
  **Hard constraint either way (E-2071):** the probe must not itself be a hook
  invocation, or it writes a row into the real database as a side effect of
  asking whether it is safe to write to the real database.
- **Where the gate runs.** Two placements are already ruled out by evidence in
  the analysis. It must run *before* `monitor.DB()` executes `schema.SQL`, since
  a gate after the connect has already let old schema land. And it must run
  *outside* the `pinnedToForeignRealDB()` test: `hook` calls `PinMainDB()` on
  every production invocation, and that pin makes `monitor.DB()` skip the schema
  apply and all five enum integrity checks — so a gate placed where those checks
  live would never fire for hooks, which is the exact population it exists to
  catch.

## 5. Assigned elsewhere, deliberately not decided here

- **What hooks do during the land window** — the seconds where the database is
  migrated and the installed binary is not, when every hook on the machine has
  nothing valid to run and last time printed fifty near-identical errors after a
  *successful* land. Mike confirmed this belongs to **E-2020**, whose
  description already commits to the shape: silent no-op plus a recorded fault
  on the hook surface, following E-1962, while interactive surfaces refuse
  loudly.

  One observation for whoever takes E-2020: during that window the *landing
  worktree's* binary is the only one on the machine whose version matches the
  migrated database. A rule of "spawn the worktree binary when versions match"
  routes that worktree's own hooks correctly for free. It does nothing for the
  other ~130 worktrees.

## 6. The distribution constraint, and what it costs

A fix cannot ride into existing worktrees as an ordinary commit:

- `bin/` is gitignored, so no commit carries a binary.
- `.claude/settings.json` — the file that decides which binary runs — is tracked
  but carries `git update-index --skip-worktree` in every worktree (E-998), so
  git deliberately will not update it there. This is not a detail; it is how the
  e-1947 land failed.

`endless worktree sync` now exists (E-2090, landed) and rebases task worktrees
onto the default branch, reporting drift and closing it with `--apply`. **But a
rebase cannot deliver a change to a skip-worktree file.** So the existing
population is reachable only if skip-worktree is lifted first, or if the sweep
writes `.claude/settings.json` directly, outside git.

Mike reports a task exists to remove skip-worktree or move to
`settings.local.json`; it could not be located by search. E-2059 folds
"settings.json / skip-worktree isolation" in as one more artifact kind rather
than a special case, which is the nearest live owner. **If no such task exists,
one is needed** — it is a hard dependency of the sweep.

The durable form, worth stating because it is what makes this the last time the
question has to be answered by touching ~96 files: **a worktree's settings
should stop naming a binary path at all.** Once they always name main's binary,
every future decision about which binary runs lives inside a binary that can be
updated.

## 7. Follow-ups spawned

- **ED-1595** — the decision above. `proposed`; needs Mike's accept or reject.
- **E-2166** — implement the inversion, the declaration, and the sweep.
- **E-2167** — retract the false comment in the e-1929 change file (find it with
  `grep -rn "never entered in practice" internal/`), which states that the
  window between schema application and the binary swap "is never entered in
  practice." It is entered; the analysis documents it being entered. That file
  is where later change files copy their ordering reasoning from, so the false
  claim propagates until it is corrected at the source.
