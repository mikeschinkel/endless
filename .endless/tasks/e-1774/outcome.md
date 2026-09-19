# E-1774 — Concern taxonomy → proposed `task report --xml` element set

Grounds E-1771's `--xml` payload in what agents **actually** report at handoff
(28 recent sessions sampled), then partitions every observed concern along **two
axes** and names the ceremony the schema deliberately leaves unrepresentable.
Recommendation only; E-1771 finalizes. Converged with Mike during the session.

## Method

- **Inventory:** 183 Claude Code transcripts across 141 endless project dirs
  (`~/.claude/projects/*endless*/…jsonl`; main checkout + one dir per `e-NNNN`
  worktree). Final handoff = last `type:"assistant"` message with a non-empty
  `text` block.
- **Sampled the 28 most recent** sessions' final handoff/report messages and
  classified the distinct concerns each raised. The taxonomy stabilized by
  ~sample 15; later samples only reconfirmed it.
- **Grounded every "computed" claim in shipped code:**
  - Git anomalies: `monitor.WorktreeAnomalies`
    (`internal/monitor/worktree_anomalies.go:46`) — kinds `uncommitted`,
    `detached-head`, `branch-mismatch`, `prunable`; `AutoManagedStatusGlobs`
    (`:37`) filters endless's own churn; NOT anomalies: commits ahead of main,
    git tags. **No external-service state is computed anywhere** — both surfaces
    (`worktree check`, `session status`) inspect only local git.
  - Test results: CTRF (Common Test Report Format) — E-1604's vendored
    CTRF-subset writer normalizes `go test -json`, pytest JSON, and TAP. It is
    **shipped product**, not self-dev tooling: `internal/verify/{ctrf,gotest,
    pytest,normalize}.go`, `internal/verifycmd/report.go`, `verify_cmd.py`.
    E-1603 (runner) and E-1604 (writer) are landed/`assumed`. Only endless's
    *own choice* of suites (`tests/tasks/e-NNNN-verify.sh`) is self-dev.
  - Handoff templates (`internal/templatecmd/templates/handoff/*.tmpl` +
    `_close.tmpl`) already enumerate reporting categories and already carry the
    anti-ceremony rules.

## The two axes

The first-cut taxonomy had one axis — *who supplies the value*. Co-design added a
second, orthogonal one — *does the fact get persisted as structured DB state*.
This is the `session_tasks` → `session status` trajectory: `session_tasks` began
as "tasks a session mentioned" and became the backbone of `session status`.
Capturing objective facts as structured rows now enables deterministic tooling
later. The two axes are independent, and their intersection yields the headline
result:

> **The "validated-value" middle bucket collapses to empty.** Once you refuse to
> ask the agent for anything computable, every constrained field (test result,
> external state, git facts) turned out to be computable — or
> computable-given-capture. What remains for the agent to supply is *only* gated
> free text. The schema is **computed+captured on one side, gated free-text on
> the other, with no editorializing middle.**

## Observed concern taxonomy (28 handoffs)

| # | Concern | Freq | Disposition |
|---|---------|------|-------------|
| A | Verify command (`esu && ./tests/tasks/e-NNNN-verify.sh`) | high | computed (task id) |
| B | Return line (`tmux …`) | high | **removed — legacy** |
| C | Test result ("908 passed", "18/18", spec-first RED) | high | computed (CTRF) + captured |
| D | Git/worktree anomalies (mostly "tree clean") | high | computed; real cases → `<note>` |
| E | Commit state (sha, # ahead, landed?) | med | computed (git) + captured |
| F | State outside endless ("none — no CI") | med | **removed — pure ceremony** |
| G | Deliverable location | med | computed (outcome on task) |
| H | Follow-ups spawned (ids + status) | high | computed (link query) + captured |
| I | Next-action commands | med | computed (from follow-up status) |
| J | This-task status recap | high | computed / no element |
| K | Epic children breakdown | med | computed (tree) |
| L | Open question / awaiting-decision | high | **agent free-text `<question>`** |
| M | Out-of-band discovery | med | **agent free-text `<note>`** |
| N | Self-correction / retraction | med | no element (belongs in chat) |
| O | "What changed" prose | med | file-list computed; why → plan/commit |
| P | Gitignored-artifact notes | med | no element (pure ceremony) |

Ceremony (naming an empty category, the "inventory of the silence," the
self-congratulatory envelope) recurs across the sample exactly as E-1760's
worked example predicted — e-1625 / e-1417 / e-1653 / e-1721 each pad a real
2–3-item handoff with "working tree clean / nothing touched / no CI."

## Proposed schema

### 1. Agent-supplied `--xml` payload — gated free text ONLY
```xml
<report>
  <note kind="anomaly|discovery">MEMORY files edited outside the branch won't land</note>
  <question>Spawn Wave 1 in a go-pkgs worktree, or develop go-cambria directly?</question>
</report>
```
- `<note>` (D-real, M) — repeats; attribute `kind`. The one escape hatch E-1771
  guards with the scoped per-entry Haiku check ("a real thing the reviewer
  couldn't compute, or the absence of a problem?"). Specimens: "MEMORY files
  edited outside the branch won't land"; "base is behind main"; "E-1648 landed
  mid-session"; the e-1625 rebase-conflict-resolution narrative.
- `<question>` (L) — repeats. Genuine "done, but I need your call on X." *Not*
  ceremony, so a **different** per-entry check ("a real decision you need, or
  restating settled state?"), not the anomaly check. → Flag for E-1771:
  reconcile with E-1773's mid-session `FULL STATUS` bypass.
- **No validated-value elements.** The bucket is empty (see headline).

### 2. Computed by the command AND captured as structured rows
The agent types none of this. **Persist into queryable relational tables — a
`session_reports` row plus child rows — never a serialized XML/text blob.** A
blob satisfies `task report`'s own rendering but defeats the point of capture:
future tooling (like `session status` over `session_tasks`) must be able to
*query* these facts, so they must land as normalized columns/rows.

- **tests** — from the CTRF result. One captured **row per test**; the rendered
  handoff shows only the rollup, and only when `failed > 0`:
  ```xml
  <tests scope="task" total="19">
    <passed count="17"/>
    <failed count="2"><test id="TestFooBar"/><test id="TestBaz"/></failed>
    <skipped count="0"/>
  </tests>
  ```
  - `scope="task|project"` splits the task's own verify suite from the
    project-wide regression run (templates ask for both). Repeats per scope.
  - **Nested**, not flat attributes: `failed`/`skipped` carry the failing test
    *identifiers* (entities with children), so the counts can't be leaf
    attributes without dropping the detail capture wants. `passed` alone would be
    a scalar, but mixing one flat + two nested is the ugly inconsistency — nest
    all three.
  - Caveat: task-scope CTRF exists via the shipped runner; **project-scope has no
    structured capture yet** — either the report command reads/produces it, or a
    follow-up makes the regression run emit counts. Not a reason to hand it to
    the agent.
- **regression** — derived, never reported by the agent. A single session lacks a
  trustworthy baseline (may never have run the suite on main; the test may be
  new; the failure may be environmental), so asking the agent to classify is
  asking it to *infer* — the exact crack where "probably not my fault" self-cover
  seeps in. Instead, diff a run against the **last-green baseline in captured
  history**; regression falls out deterministically. Frame it as a
  **recoverability signal** — "this test has a known-green ancestor, so a working
  version exists in VCS" — not blame; most meaningful at `scope="project"`, where
  a newly-red suite means recoverable prior code exists regardless of cause.
  Exists only once test-run capture exists (another payoff of the capture axis).
- **git/worktree anomalies** (D, E, P) — `WorktreeAnomalies`; empty ⇒ zero
  output. Erases "tree clean" / commit-delta / gitignored-artifact ceremony.
- **commit state** (E) — sha, # ahead, landed? — git-computed + captured.
- **verify command** (A), **return line removed** (B), **follow-ups + next
  actions** (H, I), **deliverable pointer** (G), **this-task status** (J), **epic
  children** (K) — all computed/queryable; agent supplies nothing.

### 3. Removed / given NO element (unrepresentable — this is the point)
- **Return line (B)** — legacy (`spawner_task` / `return_anchor` decouple).
- **External state (F)** — no element. A search of ~60 recent sessions found F is
  *pure ceremony* ("No external state (CI, services) involved", "No external/CI
  state involved", ×several); **zero genuine positives** — no session actually
  had external state in play. The rich kind-set in an earlier draft was
  extrapolated from the template line "flag external state if in play," not from
  data. Recommendation: no element; the rare real case rides
  `<note kind="discovery">`. **Also strike the "flag external state" line from
  the handoff templates** — it manufactures the negative.
- Clean-state confirmations, landed-vs-worktree delta ("you own the land"),
  this-task status recap, gitignored-artifact notes (P), "what changed" prose
  (O), self-correction / retraction (N).
- The **envelope** ("Here's my handoff! 🎉", "Everything checks out. Done."):
  no free-text wrapper exists — the command assembles the message and the agent
  prints it verbatim, so the "inventory of the silence" meta-ceremony (E-1760
  Turn 3) is structurally impossible, not merely discouraged.

## Terminology (fixed during co-design)
- Test tokens plain: `passed` / `failed` / `skipped` (not `green` / `all-pass`).
- No "expected-failures" vs "regression" **agent** field — to the agent a failure
  is a failure; the never-worked-vs-worked-then-broke distinction is *computed*
  from baseline history, not asserted.
- CTRF = Common Test Report Format.

## Illustrative payloads
Normal, clean path (the overwhelming majority) — the agent supplies an empty
report; the command computes and renders everything (anomalies → silence, verify
command, follow-ups, status):
```xml
<report/>
```
A session with a genuine out-of-band fact and an open decision:
```xml
<report>
  <note kind="anomaly">MEMORY files edited outside the branch won't land with the merge</note>
  <question>Develop go-cambria in a go-pkgs worktree, or directly in the repo?</question>
</report>
```
The common case makes **zero** Haiku calls (no free text present), so
structure-and-check together cost nothing on the normal path. The payload only
grows when there is a genuinely non-computable thing to say.

## Follow-up surfaced (not filed — awaiting Mike's call)
E-1596 (epic) reads `unplanned` despite five `assumed` children + one
`unverified` + seven `unplanned`. Confirmed against `internal/events/
epic_derivation.go`: this is the E-1541 rule working **as written** — precedence
`underway > ready > submitted > unplanned > completed` lets any `unplanned` child
outrank the evidence-of-progress from terminal/in-flight children. Not a
derivation bug; a **rule-semantics** question (should an epic with any
terminal/in-flight child floor at `underway`?). Left for Mike to decide whether
to file as a derivation-rule revisit.
