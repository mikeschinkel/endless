# Show landed non-terminal tasks as `⁇ other?` (not `▶ do`) in `session status` / monitor

> Naming note: the command was renamed from `session next` to `session status`
> (one-shot) plus the live `monitor` (E-1688). Both render from the same
> `monitor.SessionStatusRows` query + `sessionstatuscmd` renderer, so this single
> fix covers both.

## Problem
`endless session status` / the live monitor offered an **already-landed epic**
(E-1461) as a fresh `▶ do`, which caused a duplicate coordinator session to be
spawned.

Root-cause chain (confirmed against the db-ledger + code):

1. **E-1541 epic-status auto-derivation** (`internal/events/epic_derivation.go`)
   re-derives an epic's status as a pure function of its children:
   `any child underway → underway`, else `any ready → ready`, else
   `any unplanned → unplanned`, else `all terminal → completed`. As E-1461's
   children moved across statuses in parallel sessions, the epic legitimately
   derived `ready` whenever no child was underway but ≥1 child was ready. This
   derivation is correct and is **not** changed here.
2. **`internal/sessionstatuscmd/session_status.go::classify()`** maps
   `status == "ready"` → `actDo` (`▶`) with no regard for whether the task has
   already landed.
3. **`internal/monitor/session_status.go::SessionStatusRows`** never selected the
   landed fact (a join to the `task_landings` table), so a landed task was
   indistinguishable from an un-landed one.

## Decision (Mike, 2026-06-30)
A landed task is NOT omitted — it is still in a non-terminal status and should
stay visible — but it must NOT carry an actionable icon (it has been merged; the
normal do/plan/verify verbs no longer apply). It is shown in the catch-all
"other?" bucket instead. Re-acting on a landed task is then a deliberate human
read, not a tool-suggested action.

- **Scope:** a landed task in ANY non-terminal status routes to `other?`
  (ready→not `▶`, unplanned→not `✎`, unverified→not `☑`, underway→not `◷`).
- **No merge of existing actions:** `◷ orphan` (STALE = underway, no live
  session) is kept as a distinct action. Landed tasks reuse the existing
  catch-all action (today the silent `·` default).
- **Icon:** the catch-all action's glyph changes from `·` to `⁇`. **No legend
  entry is added** — the `legend` line is already at its width budget, and this
  bucket stays undocumented exactly as the silent `·` was. `◷ orphan` is
  untouched.
- Decoration precedence is unchanged: focal (`●`) / parent (`↑`) / in-flight
  (`⟳`) still win over the landed check, so a landed task a live session is
  actively on still reads `⟳ doing`.

## Implementation

### 1. `internal/monitor/session_status.go`
- Add `Landed bool` to `SessionStatusRow` (classify now consumes it).
- In the `enr` CTE add a computed column:
  ```sql
  EXISTS(SELECT 1 FROM task_landings tl WHERE tl.task_id = b.id) AS landed
  ```
- Add `landed` to the outer SELECT column list and `&r.Landed` to the `Scan`.
- **Do NOT change the final WHERE filter.** Landed non-terminal rows must stay
  visible; they already pass `status NOT IN (<terminalStatusSet>)`. Landed
  *terminal* rows remain hidden unless focal/parent/`--all`, as today.

### 2. `internal/sessionstatuscmd/session_status.go`
- In `classify()`, after the focal/parent/in-flight decoration switch and BEFORE
  the status switch, add:
  ```go
  if r.Landed {
      return actOther // landed: merged work, no actionable verb → ⁇ other?
  }
  ```
  The status switch is otherwise unchanged — `underway/in_progress` still →
  `actOrphan` for non-landed tasks.
- In `(action).icon()`, change the `actOther`/default glyph from `·` to `⁇`.
  `actOrphan` stays `◷`.
- **Do NOT modify the `legend` constant** — it is already at its width budget and
  this bucket is intentionally undocumented (as the silent `·` already was).
- **Width caveat:** `⁇` is U+2047 and may measure/render as ambiguous- or
  double-width. This table aligns columns with `go-runewidth`. Verify
  `runewidth.StringWidth("⁇") == 1` AND that it renders single-width in the real
  terminal; if it widens the action column, fall back to `·` (keep the
  `actOther` routing either way — the glyph is the only thing in question).

## Tests
- `internal/monitor/session_status_test.go`: add helper
  `snLanding(t, db, id, taskID int64, branch, sha string)` inserting one
  `task_landings` row (cols `id, task_id, session_id, branch, merge_commit_sha`;
  `landed_at` defaults). New test `TestSessionStatusRows_LandedColumn`: a landed
  non-terminal task is STILL returned (not omitted) and its `Landed` field is
  true; an un-landed task has `Landed == false`.
- `internal/sessionstatuscmd/session_status_test.go` (classify is pure /
  DB-free): assert
  - landed + `ready` → `actOther` (NOT `actDo`),
  - landed + `unverified` → `actOther` (NOT `actVerify`),
  - landed + `unplanned` → `actOther`,
  - non-landed + `ready` → `actDo` (unchanged),
  - non-landed + `underway` (not in-flight) → `actOrphan` (unchanged),
  - landed + in-flight → `actDoing` (decoration still wins).
  - `actOther.icon() == "⁇"`.

## Verify
- `just build`, then
  `go test ./internal/monitor/... ./internal/sessionstatuscmd/...` — new tests
  pass, existing tests unchanged.
- Manual (sandbox DB): insert a `task_landings` row for a `ready` task, then run
  `endless session status` from a session where it is non-focal — confirm it
  shows `⁇` not `▶`, and still appears (not omitted).

## Out of scope
- The E-1541 epic-status derivation behavior is correct; unchanged.
- No merging/removal of `◷ orphan`; STALE stays a distinct signal.
- No new "landed" marker column on focal/parent rows (separate task if wanted).
