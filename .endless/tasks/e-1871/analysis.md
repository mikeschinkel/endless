## Symptom

In `endless session status --all`, an ordinary done task renders with the ⁇ glyph, and the
legend then advertises "⁇ unknown". ⁇ is the renderer's should-never-happen marker — the
comment on `actUnknown` says its appearance "flags an unhandled status slipping through" —
so it reads as a bug in the tool on a row that is in fact perfectly normal.

## Reproduce

Seed an epic with done children (one landed, one not) and render with `--all`:

```
● this  ⏚ landed  ⁇ unknown  ✓ done
● T E-900  1 the epic I am on
⏚ T E-905  ✓ child: confirmed AND landed
⏚ T E-907  1 child: ready AND landed
⁇ T E-901  ✓ child: confirmed, never landed
⁇ T E-902  ✓ child: declined
```

E-901 and E-902 are unremarkable — one confirmed, one declined — but both get the
"something is wrong here" glyph.

## Why

`classify()` (internal/sessionstatuscmd/session_status.go) switches on status and has NO
case for any of the five terminal statuses — `confirmed`, `assumed`, `declined`, `obsolete`,
`completed`. They fall through to `default:` → `actUnknown` (⁇).

A row hits this whenever ALL of these hold:
  - status is terminal, AND
  - it is not focal / parent / from / in-flight (those decorations are checked first and win), AND
  - it has no `task_landings` row (a landed task is caught one branch earlier → `actLanded` ⏚).

Terminal rows are filtered out of the view unless `--all` is passed, which is why this stays
hidden day to day. But under `--all` the affected set is not marginal:
  - `declined` and `obsolete` tasks never land by definition, so they are ALWAYS ⁇;
  - so is anything confirmed/assumed whose work never went through `worktree land`.

## Why it matters

⁇ is load-bearing as a diagnostic: it is supposed to mean "classify() met a status it does
not know about." Firing it on the most ordinary rows in the ledger destroys that signal —
a genuinely unhandled status would now be invisible in the noise.

## Decisions the fix needs

1. What should a terminal, non-landed row render as? Options: its own action + glyph
   (e.g. ✓/done, distinct from ⏚ landed), or fold into `actLanded` (wrong — "landed" and
   "done" are not the same claim, and ⏚ would then lie about a declined task), or a new
   `actDone`. Needs a call.
2. Whether `blocked` belongs in the same cleanup. `TestClassify` currently pins
   `Status: "blocked"` as the "unrecognized status is unknown" case, yet `blocked` is a
   documented status in the guide's status table — so the test encodes the same gap.
3. Sort rank: wherever the new action lands in the `action` enum determines where done rows
   sort under `--all` (`sortRows` ranks on the enum). Today they sort last, as ⁇.

## Adjacent (same change, trivial)

internal/monitor/session_status.go:36 still documents `Landed` as routing "a landed
non-terminal task to the catch-all ⁇ bucket". That has been wrong since E-1750 split ⁇ into
⏚ landed + ⁇ unknown; classify() returns `actLanded`. Fix the comment while in here.

## Provenance

Found while reproducing E-1707 (a non-focal `completed` + ◆ row rendered `⁇ T◆E-NNN ✓ …`),
and deliberately fenced out of it — E-1707 changed only dim/bold, never classification.
No test covers this: `TestClassify` has no terminal-status case, and `TestBuildLegend`'s
terminal case sets `IsFocal`, so it exits classify() at the first decoration and never
reaches the switch.
