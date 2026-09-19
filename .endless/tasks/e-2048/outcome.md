# E-2048 — self-dev isolation review: findings, rulings, and the questions that block it

Reviewed 2026-08-24. Vocabulary and the surviving set are also written into
E-2050's plan (`endless task show E-2050 --text`), which is where future
sessions will look.

---

## 1. Vocabulary — one term per axis

"Route/routing" meant three mechanisms. Use these; **"route" is banned as a
verb**, and a title in this area must name which axis it changes.

| Term | Means | Chosen by | Resolver today |
|---|---|---|---|
| **DB target** | which `endless.db` a command opens | `--db main\|sandbox` | `config.apply_db_choice` (Py), `monitor.ConsumeDBContextFlag` (Go) |
| **artifact root** | which tree a command writes non-DB files into, and whether it git-commits them | nothing — each writer decides for itself | `eventcmd.ledgerRoot()`, for the ledger **only** |
| **binary selection** | which `endless-go`/`endless` build executes | cwd / hook config | E-1972 — **not this epic; coordinate only** |

**No live task in this epic uses the banned word.** The plan named E-1733's
title as the known offender; it reads "Decide which ledger a write lands in
(main vs sandbox vs test) and whether it is committed" and is already clean.
The only surviving user was E-1839's title, and E-1839 is now obsolete (§3).

## 2. The root cause — one finding that explains all the rest

There is no fourth term for the thing the three axes consult, because **it is
not a value**. "Am I in a disposable context?" is a *predicate re-derived from
a path shape* at each site that cares: `monitor.IsSandboxActive()` asks whether
`ConfigDir()` sits under `CacheDir()/sandboxes/` (ED-1528, "the resolved
endless.db config dir is the sole sandbox switch").

Three consequences, and they are the three ways this area keeps reopening:

1. **A writer that never calls the predicate is unisolated by default, and
   silently so.** `IsSandboxActive()` has six non-test call sites: five in
   `internal/eventcmd/event.go`, and one in `monitor.ForceRealDB` (the hook's
   escape hatch). Python has no equivalent — `_mirror_decision_body` branches on
   `worktree_root_for_cwd()`, never on the DB target. So the ledger got isolated
   and every other artifact writer did not — which is E-2035, filed as a
   consequence of E-1729 having been "applied at one call site instead of made
   a rule."
2. **The predicate is invisible to the agent**, so the only affordance is
   remembering `--db main`. That was E-1839's whole complaint, and it is a
   symptom of this, not a separate problem.
3. **Any change to where the sandbox lives silently invalidates every
   consumer.** ED-1554 is exactly that change, and it is already accepted-in-
   principle and unimplemented (Q1).

**The fix is to make the isolation context an explicit resolved value that
every writer consults, rather than a path shape each writer may or may not
re-derive.** E-2035 is that task; it should be scoped as such rather than as a
doc-mirror bug.

## 3. Ruling on E-1733 (Step 2) — **superseded, in its location premise only**

Recorded: `E-1733 replaced_by E-2048`. Status held at `completed` — it shipped
and its outcome is the historical record.

**What survives, and governs this epic:** ED-1525's routing principle — the
ledger is the active DB's WAL and lives next to it; one in-code resolver
returns the target (root + commit-or-not); reads are symmetric. Generalized
from "the ledger" to "every artifact," this is the epic's one rule.

**What is superseded:** ED-1525's *location* premise. It rejected a
worktree-hosted, gitignored sandbox because it "lives inside a checkout of the
real repo, so it is guarded-against rather than structurally unable to ride the
land rebase into main; splits the DB from its WAL lifecycle-wise." Mike
concurred, 2026-07-04. **ED-1554 (2026-08-13, from E-1958, implementation
E-1964) adopts exactly the rejected option**: the sandbox moves to
`<worktree>/.endless/sandbox/`, gitignored, lifetime equal to the worktree's,
and the `XDG_CONFIG_HOME` injection and `sandbox bind` are deleted.

ED-1525 is `accepted`; ED-1554 is `proposed`; **the two contradict each other
and are not linked**. ED-1528's sole-switch predicate is collateral: it keys on
`CacheDir()/sandboxes/`, which ED-1554 removes.

**Verified: ED-1525's hazard model was right, and the hazard has already
fired** — see §4.4. That is an argument for taking Q1 seriously, not for
reversing ED-1554.

## 4. Evidence gathered

### 4.1 E-1729's isolation works, and the leak has stopped

- 23 of 161 sandboxes now hold their own `db-ledger/` directory. E-1733
  measured **zero** before the fix.
- Main's `.endless/db-ledger/` has **no working-tree dirt**.
- The newest change to any foreign-node segment on main is 2026-07-31
  (commit `8e5c3f16`, "Endless: auto-record session activity"), and its entire
  content is **two blank lines** appended to `db-entries-fec1-000001.jsonl`.
  No event has leaked since E-1729 landed on 2026-07-05.
  (The reader skips empty lines, so this is cosmetic. It is worth noting only
  as evidence that land's auto-commit commits ledger dirt without looking at
  it — which is E-1272's premise, already `underway`.)

### 4.2 E-1730's residue is not zero — but its stated justification is wrong

Real ledger today: **8,313 lines across 56 segment files.** 7,240 belong to the
real node `a7f3`; **1,073 lines across 41 foreign node ids** are sandbox
leakage. That is the same 41 nodes and the same ~1,071 lines E-1733 measured —
now 13% of the ledger rather than 23%, because the ledger grew and the leak did
not. **E-1730 is still needed. The count is not zero.**

But E-1730's description says the leaked lines "produce the UNIQUE tasks.id / FK
noise in validate-db." Measured against the real ledger today
(`endless-go event validate-db`, read-only):

| Symptom | Count | Excising foreign-node lines fixes it? |
|---|---:|---|
| `UNIQUE constraint failed: tasks.id` | 649 | **Mostly.** 595 of 657 duplicate-id create events come from foreign nodes; 62 do not. |
| `FOREIGN KEY constraint failed` | 454 | **No** — that is E-1728 (projector skips session events; pre-ledger tasks absent). |
| Missing tasks (only in projection) | 113 | **Partly** — 96 are sandbox-namespace ids (<200); 17 are real-namespace. |
| Field mismatches | 309 | **No** — dominated by status renames never upcast on replay (`verify`→`unverified` ×43, `needs_plan`→`unplanned`/`ready`, `in_progress`→`underway` ×11). E-1935 / E-1671 / E-1674. |

**E-1730 should be re-scoped to what it actually delivers**: remove 1,073 known-
bogus lines and 96 phantom tasks from the ledger. It will not make `validate-db`
clean, and it should not be blocked on, or credited with, doing so.

### 4.3 E-1665's hazard is NOT gone — and it is not a testing-only hazard

There is no `emit --dry-run` and no read-only verify mode in `internal/eventcmd`.
The structural cause is the write order in `event.go`:

```
writer.Append(line)          // line is in the real ledger
CommitLedgerSegment(...)     // line is committed to main
execAndCommit(&evt, emit)    // the SQL mutation runs — and may fail
```

The mutation runs **after** the line is appended and committed, and the create
path's `rollback()` is not called on that error. Any executor-level rejection
therefore leaves a permanent, committed orphan line in the real ledger.

**It has fired at least 7 times in the real ledger.** Seven task ids carry two
`task.created` events from the real node with no `task.deleted` between them:

- **E-1119** — `task.created` "Add Foo" with `phase: "foo"`; the projector still
  reports `invalid phase "foo"` on every replay. The next create reuses id 1119
  for a different task.
- **E-1413** and **E-1415** — the *same title* created twice, 3 seconds apart,
  same session 463. A retry after the first emit's mutation failed.
- E-1178, E-1181, E-1182, E-1477 — same shape.

E-1182 is the visible cost: replay produces "Test child B" where the DB holds
"Auto-inject shell-init into endless-sandbox enter subshell."

**E-1665 was retitled** from "Prevent direct `endless-go event emit` from
appending to the real ledger during testing" to **"Fix event emit committing a
ledger line for a mutation that then fails."** The old title scoped it to
hand-run testing; the defect is on the normal path. A `--dry-run` flag would not
have prevented any of the seven.

### 4.4 E-2035's hazard already reached main

E-2035 reported a sandbox-routed `decision add` writing junk mirrors and two
commits onto a *task branch*. **Those commits are on `main` now.**

`.endless/decisions/ED-1.md` — 0 bytes, committed 2026-08-20 12:48:39 in
`239d7a46` ("Endless: add decision ED-1") and `f05ecd81` ("update decision
ED-1"), both on `main`. There is no decision ED-1 in the real DB; it is the
only orphaned decision mirror of the 145 present.

Two things follow:

- ED-1525's rejected-option reasoning was correct on the merits — worktree-
  hosted endless state *is* only guarded against riding the land rebase into
  main, and it rode in.
- **E-1736's land gate is under-scoped.** It refuses branch commits touching
  `.endless/db-ledger/` only. `.endless/decisions/` and `.endless/plans/` are
  not covered, which is the gap ED-1 came through. See Q4.

E-2035's premise is otherwise confirmed live: no Python artifact writer
(`decision_cmd._mirror_decision_body`, `_commit_doc_on_main`,
`task_cmd._mirror_doc_to_worktree`, `worktree_cmd._commit_doc_in_worktree`,
`_materialize_task_docs`) consults the DB target at all.

## 5. What was closed (Step 3)

| Task | Ruling |
|---|---|
| **E-1733** | `replaced_by E-2048`, status held at `completed`. Superseded in its location premise (§3). |
| **E-1839** | `replaced_by E-2048` → `obsolete`. Its question — "how do we eliminate the recurring sandbox-vs-real confusion?" — is answered by §2: the confusion is a symptom of an implicit, path-derived isolation context. The product fix is E-2035's resolver, not a signalling change. |
| **E-2049** | Already `obsolete`/`replaced_by E-2048`. Linked `relates_to E-1971` so its forensics stay reachable. See Q5. |
| **E-1665** | **Kept**, retitled, hazard confirmed live (§4.3). |
| **E-1730** | **Kept**, residue re-measured at 1,073 lines / 41 nodes; justification corrected (§4.2). |
| **E-2035** | **Kept.** The load-bearing task. Premise confirmed and worsened (§4.4). |
| E-1281, E-1729, E-1737, E-1736 | Shipped. Left alone. |

**Task count under E-2050: 5 → 3** (E-1665, E-1730, E-2035), plus this review
completing. No new tasks were filed.

## 6. Deliberately NOT absorbed

Each shares a boundary with this epic and is a separate body. Naming them is
the point — three sessions in one day filed into this area because they could
not tell where it ended.

- **E-1972** — binary selection (axis C). Per the plan's constraint.
- **E-1964 / ED-1554** — where the sandbox lives. Owns Q1; this epic consumes
  its answer.
- **E-1935** and children (**E-1728**, **E-1716**, **E-1717**, **E-1883**) —
  making a ledger rebuild trustworthy. §4.2's FK and mismatch counts belong
  here, not to E-1730.
- **E-1897 / E-1045 / E-995** — test-run isolation. **Same invariant, different
  subject**: E-1897 states it as "a test or verify run must not touch the
  user's real machine state"; E-2050 states it as "endless developing endless
  must not touch the real database, ledger, or checkout." See Q6.
- **E-1272 / E-1971 / E-1423** — land and commit machinery on the shared main
  checkout. See Q5, Q7.

---

# 7. Open questions (Step 4) — the primary deliverable

## Q1. ED-1525 vs ED-1554 — where does the sandbox live, and which lands first? **(blocks E-2035)**

`accepted` ED-1525 rejects a worktree-hosted gitignored sandbox; `proposed`
ED-1554 adopts it. E-2035 cannot be planned without knowing which root its
resolver returns, and §4.4 shows the rejected option's hazard is real.

- **(a) ED-1554 wins; E-1964 lands first, then E-2035.** Supersede ED-1525's
  location clause explicitly and link the two decisions.
- **(b) ED-1554 wins; E-2035 lands first** behind the resolver, so E-1964
  changes one function.
- **(c) ED-1525 stands; ED-1554 is revised** to keep endless's own DB/ledger
  out of the worktree while other projects' sandbox contents go in-tree.

**Recommendation: (b).** E-2035's entire point is that the location must be
resolved in one place. Building that seam first makes E-1964 a one-line change
instead of a sweep, and it is the ordering that does not depend on Q1's answer
being permanent. Whichever you pick, **ED-1525 and ED-1554 must be linked** —
they are the clearest instance of the "filed against prior art they failed to
find" failure this review exists to stop.

## Q2. Does the resolver key on an explicit context, or on the path shape? **(blocks E-2035)**

ED-1528 makes the resolved config-dir path *the* sandbox switch. §2 argues that
is the root cause.

- **(a) Explicit context.** One resolved value — `{db target, artifact root,
  may_commit}` — computed once at startup and passed to every writer. ED-1528
  is amended: the path stops being the switch and becomes one input to it.
- **(b) Keep the predicate**, and make E-2035 a discipline problem (route the
  five known Python writers through a helper, add the regression test).

**Recommendation: (a).** (b) is what E-1729 did, and E-2035 exists because it
did not hold. It also leaves Q1 able to break everything again. This is the
single decision that determines whether this epic closes or reopens a fourth
time.

## Q3. What exactly does E-1730 excise? **(blocks E-1730)**

E-1733 decided "excise once, per-line classification, not per-node." §4.2 says
the target is 1,073 lines / 41 foreign nodes, and that this will not clean
`validate-db`.

- **(a) Foreign-node sandbox lines only** — the 1,073. Leaves 62 real-node
  duplicate creates (§4.3) and everything in the E-1935 body.
- **(b) Also the 7 orphan lines from failed emits** (§4.3) and the orphaned
  `.endless/decisions/ED-1.md` (§4.4), as one "leaked artifacts" sweep.

**Recommendation: (b)**, and update E-1730's description to say plainly that
`validate-db` stays noisy afterward for reasons that belong to E-1935. (a)
leaves three separate future cleanups of the same class.

## Q4. Where does the land backstop go? **(blocks nothing; cheap either way)**

E-1736's gate covers `.endless/db-ledger/` only, and ED-1 came through
`.endless/decisions/` (§4.4).

- **(a) Widen it inside E-2035** — the resolver task already touches every
  artifact kind, so it knows the full path list.
- **(b) Reopen E-1736** (`assumed`, shipped) as `revisit`.

**Recommendation: (a).** One task, and the path list stays in one place. If the
resolver prevents the write, the gate is a backstop rather than the fix — but
§4.4 is why we want the backstop anyway.

## Q5. E-2049's write-lease and E-1971's index-lock retry — one task or two?

Both are symptoms of one cause: **main's checkout is a single uncoordinated
write point shared by every session.** E-2049 (obsolete) has the forensics —
8/8 land retries defeated by one live session's bulk emits, twice.

- **(a) E-1971 absorbs it** and is re-scoped to "serialize writes to main's
  checkout," carrying both symptoms and E-2049's evidence.
- **(b) Keep them separate** — retry-on-lock is a small fix, the lease is a
  design.

**Recommendation: (a).** File the cause, not each symptom. I linked
`E-2049 relates_to E-1971` but did not edit E-1971 — that is your call.

## Q6. Is test-run isolation the same epic?

E-1897, E-1045 and E-995 assert the same invariant about a different subject
(§6).

- **(a) Separate epics, one shared invariant** stated once and cited by both.
- **(b) One epic** — "no disposable context writes real state" — with self-dev
  and test-runs as branches.

**Recommendation: (a).** (b) makes E-2050 unclosable. But the invariant should
be written down once, in E-2050's plan, and E-1897 should cite it — otherwise
the next session files a fourth copy.

## Q7. Where do E-1733's two seeded land findings go?

Both were seeded into this task and are **land machinery, not isolation**:

1. Land trusts `.endless/worktree.json`'s `base_branch`, which nothing
   refreshes after a manual rebase; during E-1733's recovery both suggested
   recovery paths would have rewound the branch to July.
2. Land cannot settle a branch whose content is already fully upstream — the
   zero-delta case — without hand-driven prep.

**Recommendation:** route both to **E-1272** (`underway`, already rewriting
land's recovery advice and dropping its auto-commit step) rather than filing
new tasks. Finding 1 is a correctness bug in the message E-1272 is replacing;
finding 2 is the `worktree reset` need you raised. **They are recorded here and
nowhere else — they need a home before this task's outcome stops being read.**

## Q8. Settings/override isolation (seeded, axis B)

Endless-managed skip-worktree files (E-998's `.claude/settings.json` override)
block any rebase spanning a change to the committed copy.

- Should endless's rebase-performing operations clear/restore/regenerate
  automatically for files it can prove it generated (content matches generator
  output), stopping loudly otherwise?
- Should `settings.json` support project-rooted paths rather than absolute or
  home-rooted ones, so an override is portable and an intentional edit can be
  promoted to main without path translation?

**Recommendation:** this is axis B and belongs in this epic, but only *after*
Q2. If the answer to Q2 is (a), "which files does endless generate, where, and
does it commit them" is already the resolver's question, and settings.json
becomes one more artifact kind rather than a special case. **File it as a
fourth task under E-2050 once Q2 is settled** — I did not file it now, because
filing it before Q2 would make it the special case it should not be.
