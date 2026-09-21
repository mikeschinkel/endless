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

## 6. Late correction — the pin was already gone when this was written

Written into the synthesis rather than left in chat, because an earlier draft of
this section argued at length about a distribution constraint that no longer
binds, and a future session would have implemented against it.

**The constraint as drafted:** a fix cannot ride into existing worktrees as an
ordinary commit, because `bin/` is gitignored and `.claude/settings.json` is
tracked but carries `git update-index --skip-worktree` in every worktree (E-998),
so git deliberately will not update it there. `endless worktree sync` (E-2090)
rebases worktrees, and a rebase cannot deliver a change to a skip-worktree file —
so the ~96 pinned worktrees looked reachable only by lifting skip-worktree first
or by writing the file directly, outside git.

**Measured instead of assumed, 2026-09-21, twice on consecutive days:**

    worktrees with .claude/settings.json:     141
      carrying a "hooks" block:                 1   (e-2122, live session)
      carrying XDG_CONFIG_HOME:                 0
      with skip-worktree set on that file:      1

Compare this task's own analysis: 96 of 135 pinned on 2026-08-13. The main
checkout's copy is down to `autoMemoryEnabled` and `enabledPlugins`, and hooks
come from the user-level Claude settings file, pointing at the globally
installed `endless-go`.

**So the inversion this brainstorm was convened to decide had already happened
in practice** — arrived at by the per-worktree hooks block disappearing, not by
decision. ED-1554 (accepted) accounts for the XDG half: it deletes the
`XDG_CONFIG_HOME` injection and the sandbox bind. What removed the hooks blocks
was not determined; E-1964 and `worktree sync` rebases are both candidates.

Three consequences:

- **There is no sweep to build.** One worktree, held by a live session, is a
  single case and not a migration.
- **The real gap is the opposite of the one this task was filed for.** There is
  currently no way to run a worktree's hook binary at all, so candidate hook
  code is never exercised and E-998's purpose is entirely unserved. ED-1595's
  declaration mechanism is the whole of the remaining work.
- **E-2035's `.claude/settings.json` section is moot.** It exists because the
  file "legitimately needs a different body in every worktree — hooks pointing
  at that worktree's `bin/endless-go`, plus the `XDG_CONFIG_HOME` env block."
  Both are gone, so skip-worktree has nothing to hide and the symlink-into-the-
  sandbox design would solve a problem that no longer exists. Folded into that
  task as evidence. Its core — the artifact-root resolver — is untouched and
  still needed, and so is the separate generator bug it documents (the recipe
  reads settings from the worktree branch's HEAD, silently dropping any key
  added to the committed copy after that worktree forked).

This section is also the answer to "which task owns skip-worktree": **E-2035**,
which folded it in 2026-08-27 with the symlink direction. No separate task was
ever filed, and none is needed.

## 7. Follow-ups spawned

- **ED-1595** — the decision above. `proposed`; needs Mike's accept or reject.
- **E-2166** — build the declaration mechanism. Retitled and rewritten after the
  measurement above; the sweep it was filed with is dead work.

One task was filed and then withdrawn. **E-2167** proposed retracting a false
comment in the e-1929 change file (`grep -rn "never entered in practice"
internal/`) claiming the window between schema application and the binary swap
"is never entered in practice" — it is entered, and that file is where later
change files copy their ordering reasoning from. Mike pointed at ED-1550(1):
filing is the exception, and the default response to a finding is to say so in
chat. Noticing something true does not earn a task. E-2167 is `obsolete` and the
comment was corrected directly instead.
