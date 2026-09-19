# Plan — E-1623: shared verify-script harness + land-time-corpus audit

Governed by decision ED-1534 (verify suites are land-time tests). The existing
`tests/tasks/*.sh` scripts each copy-paste the same `report_*`/`assert_*` harness
from `e-1577-verify.sh`; and per ED-1534, mass-porting land-time scripts to
`verify.toml` is not pragmatic. So this task delivers a forward de-dup and an
audit that mines the corpus for real gaps — not a bulk port.

## 1. Shared harness lib (forward de-dup)

- Extract the duplicated `report_*`/`assert_*`/`section`/`summary` block into one
  sourced file, e.g. `tests/tasks/_harness.sh`.
- The lib emits TAP-compatible pass/fail so a sourcing script's assertions double
  as a raw-command TAP check (E-1604-normalizable) with no rewrite — every script
  stays one thin wrapper from the manifest world without being ported.
- New per-task scripts `source` the lib instead of copy-pasting; update the
  spawn/guide convention so future scripts start from it.
- Convert a few existing scripts as exemplars to prove the lib; do NOT bulk-retrofit
  the rest (ED-1534: land-time artifacts, churn with little value). E-1603/E-1758
  are excluded — E-1605 owns them as the manifest-side exemplars.

## 2. Land-time-corpus audit (find gaps, don't port)

Sweep the existing scripts; for each assertion ask "worth testing forever, or only
at land?" and bucket it:

- **1a — duplicates an existing `just test` test** → record "the manifest should
  select it" (no new test needed).
- **1b — regression-worthy behavior not yet in `just test`** → file a "promote to
  permanent test" follow-up; the verify script surfaced a real coverage gap.
- **2 — genuinely one-off land-time proof** (e.g. "these docs are byte-identical")
  → correct as-is; raw-command TAP is its home. No action.
- **3 — needs live state** (tmux/PTY/service) → confirms a Stage 3/4 substrate gap
  → file/link to the relevant stage task.

Deliverable = the catalogued findings (a table in the task outcome) plus the filed
follow-up tasks for buckets 1b and 3. The audit itself does NOT do the promotions,
ports, or fixes — those are the filed follow-ups (audit-delivers-findings).

## Boundaries

- Does NOT port the existing scripts to `verify.toml` (ED-1534; convert one only
  when a concrete need arises).
- Does NOT write the promoted permanent tests or fix Stage 3/4 gaps — it files them.
- E-1603 / E-1758 excluded (handled by E-1605).

## Verify — `tests/tasks/e-1623-verify.sh` (esu header; exit 0/1/2)

- the shared lib exists; an exemplar script that sources it (dropping its inline
  harness) still passes (exit 0) and emits TAP.
- pass/fail behavior is identical before vs. after the exemplar's conversion.
- the audit's catalogue + the follow-up tasks it files exist (findings reviewed by
  the user; not fully script-checkable).
