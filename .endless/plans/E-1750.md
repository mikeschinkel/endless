# E-1750 — Make the session-status legend dynamic

## Context

The session-status list legend is a fixed one-line `const` that hardcodes every
possible glyph. Because it must fit one line, otherwise-useful glyphs were left
out (notably `⁇`, and the `◆ dirty` marker) — the line has no room. Building the
legend from only the actions/decorations actually present in the current frame
keeps it within one line in >99% of cases and lets an omitted glyph appear
exactly when it's on screen. This also lets us split today's `⁇` catch-all (which
conflates *landed* and *unrecognized-status*) into two documented glyphs.

All work is in one Go file plus its test and a new verify script. No product
behavior outside the legend/classification changes.

## Changes — `internal/sessionstatuscmd/session_status.go`

1. **Action metadata table.** Replace the standalone `func (a action) icon()`
   with a table carrying both glyph and legend label, indexed by the `action`
   enum so enum order == legend order for free:

   ```go
   var actionMeta = [...]struct{ icon, label string }{
       actThis:    {"●", "this"},
       actParent:  {"↑", "parent"},
       actFrom:    {"↩", "from"},
       actDoing:   {"⟳", "doing"},
       actDo:      {"▶", "do"},
       actPlan:    {"✎", "plan"},
       actVerify:  {"☑", "verify"},
       actOrphan:  {"◷", "orphan"},
       actLanded:  {"⏚", "landed"},
       actUnknown: {"⁇", "unknown"},
   }
   func (a action) icon() string  { return actionMeta[a].icon }
   func (a action) label() string { return actionMeta[a].label }
   ```

2. **Split the `⁇` catch-all.** Extend the enum from `…, actOrphan, actOther`
   to `…, actOrphan, actLanded, actUnknown` (append only — do not reorder the
   existing members, since `sortRows` ranks on the enum). In `classify`:
   - `if r.Landed { return actLanded }` (was `actOther`).
   - `default: return actUnknown` (was `actOther`).
   `actUnknown` now means *only* "a status the classifier doesn't recognize" — a
   should-never-happen safety net; `⁇` finally matches its `unknown` label.
   Update the `actLanded`/`actUnknown` doc comments accordingly (drop the
   "intentionally undocumented" note — both are now in the label table).

3. **Delete `const legend`.** Add `buildLegend`:

   ```go
   // buildLegend returns the dynamic header: only the glyphs actually present in
   // rows, in enum order (actions) then fixed order (decorations), two-space-
   // joined, no group separator. Rebuilt each frame by renderTo, so `session
   // status` and `session monitor` stay byte-identical by construction.
   func buildLegend(rows []monitor.SessionStatusRow) string {
       var present [len(actionMeta)]bool
       var blocked, blocks, dirty bool
       for _, r := range rows {
           present[classify(r)] = true
           if r.BlockedByN > 0 { blocked = true }
           if r.BlocksN > 0    { blocks = true }
           if r.Dirty          { dirty = true }
       }
       var parts []string
       for a := action(0); int(a) < len(actionMeta); a++ {
           if present[a] { parts = append(parts, a.icon()+" "+a.label()) }
       }
       if blocked { parts = append(parts, "⊗ blocked") }
       if blocks  { parts = append(parts, "⏸ blocks") }
       if dirty   { parts = append(parts, "◆ dirty") }
       return strings.Join(parts, "  ")
   }
   ```

   Decoration glyphs/labels are inline string literals (the `⊗`/`⏸` match
   `blockField`, `◆` matches `dirtyMark`); a tiny local table is an acceptable
   alternative if it reads cleaner at implementation time.

4. **`renderTo`** — emit the dynamic legend, and only when there are rows to
   document. Move the legend print *below* the empty/no-task guard so the no-task
   view shows just the hint (no blank/empty legend line):

   ```go
   func renderTo(...) {
       if focal == 0 || len(rows) == 0 {
           fmt.Fprintln(w, dim(noTaskHint, color))
           return
       }
       sortRows(rows)
       fmt.Fprintln(w, dim(buildLegend(rows), color))
       // …existing block-width + per-row rendering unchanged…
   }
   ```

   Single-line intent: since only present glyphs are included there is never any
   absent-glyph padding; in the rare case the present set still exceeds width the
   terminal soft-wraps (acceptable — no truncation, which would hide a real
   glyph). No `cols` argument needed by `buildLegend`.

5. **`⏚` width guard.** Confirm `runewidth.StringWidth("⏚") == 1` (as the `⁇`
   comment already asserts) so the fixed 13-col prefix alignment holds; document
   it in the `actLanded` comment. If it measures wide, stop and reconsider the
   glyph before proceeding.

## Tests — `internal/sessionstatuscmd/session_status_test.go`

- **`TestClassify`**: retarget the existing cases — `Status:"blocked"` and the
  three `landed …` cases now expect `actUnknown` / `actLanded` respectively
  (currently `actOther`).
- **`TestActionIcons`**: add `actLanded`→`⏚`, `actUnknown`→`⁇`.
- **New `TestBuildLegend`** (table-driven, constructs rows directly): asserts, at
  minimum —
  - do+plan rows only → `"▶ do  ✎ plan"` (no orphan/verify/landed/unknown/
    blocked/blocks/dirty).
  - a `Landed` row → contains `"⏚ landed"`.
  - an unrecognized `Status` (e.g. `"blocked"`) → contains `"⁇ unknown"`.
  - `BlockedByN>0` → `"⊗ blocked"`; `BlocksN>0` → `"⏸ blocks"`; `Dirty:true` →
    `"◆ dirty"`; each absent when no such row. **This is the sole coverage of
    `◆ dirty`** (a real divergent worktree can't be seeded hermetically in bash —
    decided with Mike).
  - output contains no `"|"`.
  - a full/mixed row set orders actions in enum order then decorations.

## Verify — `tests/tasks/e-1750-verify.sh`

Model on `tests/tasks/e-1577-verify.sh` (per-check pass/fail, `ALL PASSED`
summary, exit 0 all-pass / 1 any-fail / 2 setup). Handoff to Mike is exactly:

    esu && ./tests/tasks/e-1750-verify.sh

Check groups, all folded into the one script:

1. **Legend logic (hermetic).** Run `go test ./internal/sessionstatuscmd/...` —
   green covers `TestBuildLegend` (incl. `◆ dirty`), `TestClassify`,
   `TestActionIcons`.
2. **End-to-end binary path**, seeding the sandbox DB and driving
   `./bin/endless-go session-status --task <id> [--from-session <id>] --cols <n>`
   (headless; no tmux). Build rows as children/dependents of a focal task:
   - do+plan-only focal tree → legend lists just `▶ do` / `✎ plan`, and asserts
     **absence** of orphan/verify/landed/unknown/blocked/blocks/dirty entries.
   - a landed child (`endless worktree land <id> --record-only --sha <dummy>`) →
     legend contains `⏚ landed` and the row renders `⏚`.
   - a blocked child (`task add --blocked-by <open id>`) → `⊗ blocked`; a
     blocking child (`task add --blocks <id>`) → `⏸ blocks`; each absent
     otherwise.
   - an unknown-status child (`task update --status blocked`, falling back to the
     Go test if the CLI's status guard refuses it) → `⁇ unknown`.
   - the legend contains no `|`.
   - **parity:** one `session status` frame is byte-identical to one
     `session monitor` frame for the same rows — `diff <(… --task N --cols C)
     <(… --task N --monitor --cols C)` (piped `--monitor` degrades to a single
     snapshot, so this proves the shared renderer).

Use the worktree-built binary directly (`./bin/endless-go …`, self-detects the
sandbox from cwd) and route CLI seeding through `--db sandbox` like the e-1577
model.

## Out of scope

- `internal/monitor/session_status.go`'s `⁇` mentions (SQL comments only — no
  second legend).
- The stale-binary symptom where `submitted` renders `⁇` instead of `▶` (fixed by
  rebuilding, not code here).

## Steps

1. Edit `session_status.go` (enum, `actionMeta`, `buildLegend`, `classify`,
   `renderTo`, comments).
2. Update/extend `session_status_test.go`; `just build` then
   `go test ./internal/sessionstatuscmd/...` until green.
3. Write `tests/tasks/e-1750-verify.sh`; run `esu && ./tests/tasks/e-1750-verify.sh`.
4. `endless task update E-1750 --status unverified --db main`; hand Mike the
   one-line test command + return line.
