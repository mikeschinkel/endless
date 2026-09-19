# Decide the self-dev isolation questions E-2048 left open

E-2048's outcome listed eight open questions. On re-reading, two of them are the
same decision, one is downstream of it, and five are dispositions that already
have defaults. This task carries the reduced form: **one decision, then five
confirmations.**

Read `endless task show E-2048 --outcome --db main` for the evidence behind any
claim here. Do not re-derive it.

---

## The decision

**Is "am I in a disposable context?" an explicit resolved value, or a predicate
re-derived from a path shape at each site that cares?**

Today it is the predicate. `monitor.IsSandboxActive()` asks whether `ConfigDir()`
sits under `CacheDir()/sandboxes/` (ED-1528). It has six non-test call sites,
five of them in one file, and Python has no equivalent at all — its artifact
writers branch on `worktree_root_for_cwd()`, never on the DB target. A writer
that never asks the question is unisolated by default, and silently so.

- **(a) Explicit.** One resolved value — `{db target, artifact root,
  may_commit}` — computed once and consumed by every writer. ED-1528 is amended:
  the path becomes an *input* to the answer rather than being the answer.
- **(b) Keep the predicate**, and make E-2035 a discipline problem: route the
  five known Python writers through a helper and add a regression test.

**Recommendation: (a).** (b) is precisely what E-1729 did for the ledger, and
E-2035 exists because it did not hold — the fix was applied at one call site
instead of made a rule, so the next artifact-writing feature re-broke it.

**Two things follow from (a) and do NOT need separate decisions:**

1. *Where the sandbox lives* — ED-1525 (accepted; sandbox outside any repo) vs
   ED-1554 (proposed; sandbox at `<worktree>/.endless/sandbox/`) — becomes a
   value the resolver returns. E-2035 lands first, and E-1964 then changes one
   function instead of sweeping call sites.
2. *settings.json / skip-worktree isolation* becomes one more artifact kind
   rather than a special case needing its own task.

**If (b), both of those come back as separate decisions**, and this area gets a
fourth round. That is the whole argument.

Either way: **ED-1525 and ED-1554 must be linked.** They contradict each other,
one is `accepted` and one is `proposed`, and nothing connects them. That is the
clearest instance of the "filed against prior art they failed to find" failure
this epic exists to stop.

---

## Five confirmations — yes/no, no discussion needed unless you disagree

1. **E-1730** sweeps the 7 orphan lines from failed emits and the orphaned
   `.endless/decisions/ED-1.md` in the same pass as the 1,073 leaked lines, and
   its description is corrected to say plainly that `validate-db` stays noisy
   afterward for reasons belonging to E-1935. *[recommend yes]*
2. **E-1736's land gate** gets widened to cover `.endless/decisions/` and
   `.endless/plans/` inside E-2035, rather than reopening E-1736. *[recommend
   yes]*
3. **E-1733's two seeded land findings** — land trusting a stale
   `worktree.json` `base_branch`, and land unable to settle the zero-delta case
   — route to **E-1272**, which is already rewriting land's recovery path. They
   are recorded in E-2048's outcome and nowhere else. *[recommend yes]*
4. **E-2049's write-lease evidence** folds into **E-1971**, re-scoped to
   "serialize writes to main's checkout," carrying both symptoms. *[recommend
   yes]*
5. **Test-run isolation** (E-1897 / E-1045 / E-995) stays a separate epic; the
   shared invariant is written once in E-2050's plan and cited from there.
   *[recommend yes]*

---

## Contract for this session — read this before doing anything

The output is **decisions recorded on tasks that already exist** — E-1665,
E-1730, E-2035, E-2050 — plus amendments to ED-1525 / ED-1528 / ED-1554.

**Do not file new tasks.** This area was reviewed once precisely because filing
against it one task at a time added tasks instead of removing them. If something
appears to need a new task, it is almost certainly a decision on one of the three
that already exist: say which one, and record it there.

The count in this area must go DOWN. A session that ends with more tasks than it
started with has failed, whatever else it produced.
