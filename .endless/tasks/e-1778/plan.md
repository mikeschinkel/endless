# Derive test regression by diffing runs against the last-green baseline

The thin read-only consumer that sits on top of captured test-run history. It
does NOT run tests, define the suite, or design the capture schema — it reads
what E-1783 (generic test-run substrate) and E-1777 (persisted rows) produce and
classifies each current failure by diffing against history. Frame the output as
a **recoverability signal**, never blame, and never let the agent assert it.

## Contingency (why this stays thin)

Blocked by **E-1783** (defines the protected suite, "green on main", when runs
are captured, per-test identity) and **E-1777** (persists results as queryable
rows). This plan fixes only the stable semantics — the diff and the framing —
and deliberately defers the concrete row shape and surface to those tasks. It is
implementable the moment they land, without re-litigating their designs.

## Consumed input (assumed interface, owned by E-1783/E-1777)

Per-test run rows, each carrying at minimum:
- a stable **test identity** (suite + test id),
- **outcome** (pass / fail / skip),
- the run's **commit / baseline linkage** (which revision it ran against),
- **scope** (project vs task).

E-1778 only reads these; the columns are E-1777's/E-1783's to define.

## Algorithm (the actual deliverable)

For a run R with failing set F, per failing test `t`:
- Find the most recent prior captured run in which `t` **passed** — its
  last-green ancestor.
- If one exists → `regression` (**recoverable**: a working version of `t` exists
  in VCS at that revision).
- If none exists → `never-green` (no known-good ancestor; not a regression).
- A clean run (empty F) → no signal at all (silence, per the anti-ceremony rule).

Most meaningful at **project scope**: a newly-red project suite means recoverable
prior code exists regardless of cause. Task scope is secondary.

Computed in Go (like the other `session-query` facts), never asserted by the
agent — a single session lacks a trustworthy baseline, and asking it to classify
invites "probably not my fault" self-cover.

## Output / surface (one open design call)

The signal is a per-failing-test classification + the recoverable revision. Where
it shows, to resolve at implementation once E-1777's read surface exists:
- fold into `task report`'s computed facts so the steering prompt can state it
  ("2 project tests regressed; last green at <sha>") — recommended, it reuses
  E-1771's fact→prompt path; or
- surface in `session status`.
Framing is fixed: recoverability ("a working version exists in VCS"), not blame.

## Scope boundary

Out of scope (owned elsewhere): running the suite / hook contract (E-1783), the
tier taxonomy (E-1784), the capture schema (E-1777). E-1778 is only the diff +
classification + framing on top.

## Tests

- Go unit test for the diff/classify core against seeded history: `t` passed at
  an earlier revision then failed → `regression` (with the recoverable revision);
  `t` never passed → `never-green`; empty failure set → no signal.
- Integration test once E-1777/E-1783 provide real captured rows.
- `tests/tasks/e-1778-verify.sh` (isolated env) seeding synthetic run history
  and asserting the classification.

## Verify
`esu && ./tests/tasks/e-1778-verify.sh`
