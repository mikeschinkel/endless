# Plan — vendored CTRF-subset result writer + native normalizers

Shared interface contract is in the epic's analysis (`endless task show E-1596 --analysis`,
section "Interface Contract / C" and locked decision 1). Conform to the CTRF-subset shape; don't
invent fields.

## Deliver

1. **A Go CTRF-subset writer** — structs for the subset in decision/contract C: `results.tool`,
   `results.summary {tests,passed,failed,skipped,pending,other,start,stop}`, `results.tests[]`
   with per-test `name`/`status`/`duration` (+ optional `message`, `trace`, `suite`,
   `stdout`/`stderr`, `attachments[] {name,contentType,path}`, `extra`). Field names/shapes
   match CTRF; pin to the CTRF snapshot (2025-11-24). Emit a single JSON document.
2. **Native normalizers** keyed by `format`:
   - `gotest-json`: parse the `go test -json` TestEvent stream (Action/Test/Elapsed/Output).
   - `pytest-json`: parse pytest's JSON report.
   - `tap`: parse TAP (for shell/BATS suites).
   Each maps native → the CTRF-subset structs.
3. **No runtime dependency on external CTRF reporters** — Endless reads the native streams and
   writes CTRF itself. (Don't add `go-ctrf-json-reporter`/`pytest-json-ctrf` as runtime deps.)
4. **Merge** — a verification has multiple `[[check]]` entries; normalize each by its `format`
   and merge the per-check results into ONE CTRF report (summary counts summed). (Per the doc,
   multiple CTRF documents merge.)
5. **First-class `tests`→native-filter translators** — for `gotest` and `pytest`, translate a
   check's `tests` list into the native filter command (`go test -run ...`, `pytest <nodeids>`).
   (May be split into its own task if scope grows; pairs with the [[check]] schema-revision task.)

## Notes

- Keep the subset minimal; add CTRF fields only when a concrete need appears.
- The `attachments[]`/`extra` hooks exist for non-text outcomes (screenshots, HTTP transcripts)
  but need no producer yet.
- Consumed by E-1603; used to render expected/got failure detail.

## Verify — ad-hoc verify script (interim, E-1577 style)

Until the formal verification system this epic builds is usable, verify this task with a
committed ad-hoc script `tests/tasks/e-1604-verify.sh`, modeled on the prototype
`tests/tasks/e-1577-verify.sh` (same shared helpers / section headers / ALL-PASSED summary /
exit 0 on all-pass, 1 on any failure). It asserts:

- a known `go test -json` stream, a pytest JSON sample, and a TAP sample each normalize to the
  expected CTRF-subset JSON (golden comparison).
- failures carry `message`/`trace`; summary counts match the native totals.
- output validates as a single JSON object with the pinned CTRF field names.

The underlying assertions may be Go golden tests the script invokes; the script is the uniform
pass/fail entry point. Interim location `tests/tasks/` (matches the existing prototypes); migrate
to the formal `.endless/tasks/<id>/` convention once the system lands.
