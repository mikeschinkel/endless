# E-1765 — Add a distinct `review` action so submitted tasks don't render as spawnable

## Context

E-1648 added the `submitted` status (spec-complete, awaiting human approval) but left it
rendering identically to `ready` in `session status`: `classify()` folds both into `actDo`
(`▶ do`), so a submitted task *looks* spawnable even though the gate refuses pickup. The gate
itself is correct (`task next` excludes submitted; a background session is refused a non-`ready`
claim) — only the visual signal contradicts it. This task gives `submitted` its own action —
**`⚑ review`** — so the render matches the gate.

Surfaced 2026-07-10 while Mike reviewed E-1764 (a submitted task that appeared "ready to spawn").

**Scope decisions (confirmed with Mike, 2026-07-10):**
- **Glyph = `⚑`** (U+2691 BLACK FLAG), label **`review`** — "flagged for your approval."
- **Web dashboard (original Deliverable 2) is descoped.** Mike is rebuilding the web from an
  entirely new design once the CLI is feature-complete, so a submitted-specific web fix would be
  thrown away. The web `project_detail.templ` "Next Actions" gap is left as-is; the verify script
  drops its web coverage accordingly.

## Deliverable 1 — distinct `review` action in `session status`

All edits in `internal/sessionstatuscmd/session_status.go`:

1. **Enum** (`const ( actThis … )`, lines ~43-62): insert `actReview` **between `actDo` and
   `actPlan`**. The enum is sort-rank order; this places review right after the spawnable `do`
   bucket and ahead of `plan`/`verify`, reading: *doing → do (spawnable) → review (awaiting your
   approval to become spawnable) → plan → verify → anomalies*. Inserting mid-enum is safe: ranks
   are in-memory only, and `actionMeta` / tests key by name, not by numeric value. Add a short
   doc comment on `actReview` in the style of the neighbouring members.

2. **`actionMeta`** (lines ~67-78): add `actReview: {"⚑", "review"}` in enum position.

3. **`classify()`** (lines ~398-413): split the current `case "ready", "submitted":` — return
   `actDo` for `"ready"` and add `case "submitted": return actReview`. Update the inline comment
   (the "routes to actDo (▶) alongside ready" note) to explain submitted now routes to `actReview`
   (⚑) because it is planned-but-unapproved: not spawnable, awaiting the user's approve.

4. **Legend** — no direct edit needed: `buildLegend()` iterates `actionMeta` in enum order and
   emits only present glyphs, so `⚑ review` appears automatically when a submitted row is present.

5. **`tree.go` `doPlanIDs()`** — no code change. It gathers only `actDo`/`actPlan`; submitted now
   classifies as `actReview`, so it is automatically excluded from the `--tree` implementation-
   order backlog. This is correct: a not-yet-approved task is not spawnable and does not belong in
   the do/plan backlog. Add a one-line comment noting review is intentionally excluded.

### Tests — `internal/sessionstatuscmd/session_status_test.go`

- `TestClassify`: change the existing `{"submitted is do not plan", …, actDo}` row to
  `{"submitted is review", monitor.SessionStatusRow{Status: "submitted"}, actReview}`.
- `TestActionIcons`: add `actReview: "⚑"` to the pinned-glyph map.
- `TestBuildLegend`: add a case asserting a submitted row yields `⚑ review` in the legend
  (and that a `do`+`review` mix renders `▶ do  ⚑ review` in enum order).

### Glyph width guard

`⚑` must measure `runewidth.StringWidth == 1` so the width-aware table stays aligned (same
constraint the `⏚`/`⁇`/`◆` comments cite). Verify during implementation with a scratch
`runewidth` check (or a throwaway `go test` assertion). **Fallback if `⚑` is double-width:** `☐`
(U+2610 BALLOT BOX) — proven single-width alongside `☑`. Add a `displayWidth("⚑") == 1` assertion
to `TestActionIcons` (mirroring the existing `◆` width assertion in `TestDirtyMark`) so the
guarantee is pinned.

## Deliverable 2 — per-task verify script `tests/tasks/e-1765-verify.sh`

Model exactly on `tests/tasks/e-1648-verify.sh` (same header/isolation/helpers): `set -u`,
throwaway git repo under `mktemp -d`, temp `XDG_CONFIG_HOME`/`XDG_CACHE_HOME`, `<worktree>/bin`
prepended to `PATH`, `section`/`report_pass`/`report_fail`/`summary` helpers, `ALL PASSED` on
success, exit `0`/`1`/`2`. Coverage (web checks intentionally omitted — see scope note):

- **Go unit**: `go test ./internal/sessionstatuscmd/` passes (exercises the new
  `submitted → actReview` row, the `⚑` glyph pin, and the width guard).
- **Classifier grep**: assert `classify` no longer maps `submitted` into the `actDo` arm and DOES
  have a `case "submitted":` returning `actReview` (mirrors e-1648's `test_classifier`, inverted).
- **Glyph/label present**: grep `session_status.go` for `actReview: {"⚑", "review"}` so the label
  and glyph are pinned in the source.
- **Regression**: `go test ./internal/events/ ./internal/monitor/` passes.

## Verification

After implementation, Mike verifies with exactly:

```
esu && ./tests/tasks/e-1765-verify.sh
```

→ **ALL PASSED**. Then a quick manual smell-test is possible but not required: with a seeded
submitted task, `endless session status` shows it under `⚑ review`, not `▶ do`.

## Out of scope / follow-up

- Web "Next Actions" rendering of submitted — deferred to the planned web redesign, not filed as a
  separate task (the redesign supersedes it). Will surface this to Mike in the handoff.

## Files touched

- `internal/sessionstatuscmd/session_status.go` (enum, actionMeta, classify)
- `internal/sessionstatuscmd/tree.go` (comment only)
- `internal/sessionstatuscmd/session_status_test.go` (3 test updates)
- `tests/tasks/e-1765-verify.sh` (new)

## Build / done

- `just build` after Go edits (rebuilds `bin/endless-go`; no templ change so `_templ.go`
  untouched).
- On completion: `endless task update E-1765 --status unverified --db main`, then hand Mike the
  verify command and the return line.
