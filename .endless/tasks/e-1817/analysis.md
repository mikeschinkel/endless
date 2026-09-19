AUDIT FINDINGS contributed from E-2001 (2026-08-20).

## 1. MISSING — no prohibition on committing to the main checkout

CLAUDE.md says nothing about committing to `main`. The commit-to-main policy
lives only in `endless guide orchestration`, which a session reads once at start
if at all.

Observed failure: a session hit a `worktree land` refusal ("main has
uncommitted user changes"), committed directly on `main` to clear it, and
reported having done so as though disclosure made it acceptable. The
rationalization worth naming explicitly is "it was the only way to unblock the
land" — a refused land is a STOP, and being asked to fix the blocker is not
authorization to commit.

Note this is product-wide, not self_dev, so under ED-1564 it belongs in
`endless guide`, not in CLAUDE.md. It is recorded here because this audit is
what will move it.

## 2. RESOLVED by E-2000 — the LESSONS.md destination

Superseded, kept only so a later reader does not re-derive it. The section once
directed appends to `<worktree>/.endless/LESSONS.md` while retaining the clause
"the *main checkout*, always, even when you are in a worktree" — both halves
unfollowable, and the stale half sent two sessions (E-2001, E-2002) to
`.claude/LESSONS.md` in main, where the untracked file blocked a land. E-2000
landed the fix; the section now says "the copy of the log in your own worktree"
and the contradiction is gone. **Do not act on this finding.**

## The criterion this audit was missing — ED-1564 (2026-08-20)

CLAUDE.md holds ONLY what is true here and nowhere else: `self_dev` mechanics,
LESSONS.md / memory-off, and rules specific to the owner's machine or working
style. Everything an agent on ANY Endless project needs goes to `endless guide`,
which is pulled on demand and reaches every project. Product invariants go in
code comments beside what enforces them, with at most a pointer.

Applying it to the current file gives the audit its first pass:

KEEP (self_dev / machine / owner):
- Build, Install / refresh, Worktree setup (Go builds), Worktree setup (Claude
  hook override), Self-dev DB sandbox — all describe this checkout's mechanics.
- Corrections go to LESSONS.md; memory is OFF — self_dev by construction. See
  the E-2007 caveat below before rewriting its prose.
- Uppercase `$KEYWORD` markers — the owner's hooks.
- PRODUCT — the owner's marker.

MOVE TO `endless guide`:
- Reporting to the user — the minimizer gate. Ships ON for every other project,
  so every user's agent needs it; only the "OFF in this repo" line is self_dev
  and only that line should remain.
- Verify scripts are pre-land gates, not a regression suite — a product-wide
  convention about a product-wide artifact.
- The missing commit-to-main prohibition from finding 1, if it earns its space
  at all.

MOVE TO CODE COMMENTS (with at most a pointer left behind):
- Which agent harness is this? (E-1962) — implementation detail of
  `internal/agentenv`, already documented at length in that package.
- One project-path spelling (E-2002) — an invariant of
  `monitor.NormalizeProjectPath`, which already carries the reasoning.

DELETE OUTRIGHT:
- Task status lifecycle — reproduced verbatim from `endless guide`, mermaid
  diagram and all. Pure duplication of the canonical source, and exactly the
  failure mode ED-1564 names: the copy goes stale silently.

## Scope: this audit does NOT assume E-2007

E-2007 replaces the flat `.endless/LESSONS.md` with a lessons table, events and
per-lesson mirrors. It is not being started now, and this task must not wait on
it or pre-write for it. Audit the LESSONS.md section AS IT STANDS — flat file,
appended in the session's own worktree.

The practical consequence is a bound on effort, not a change of direction:
E-2007 will rewrite that section's MECHANISM prose (which file, appended how,
committed where), so do not spend judgement polishing those sentences. Minimize
the section to the invariants that survive any mechanism — memory is off and why,
corrections are recorded unconditionally, the log is write-only — and let the
mechanism be the short, replaceable part. That is what ED-1564 asks for
independently, so the two agree.

A SECOND CLAUDE.md cleanup is expected after E-2007 lands. This task's output is
therefore a pass, not the final word, and a later session must not treat the
minimized file as settled.



## What this task hands off to (owner, 2026-08-20)

Sequence is research -> (maybe brainstorm) -> todo, and THIS TASK DECIDES WHICH.
The audit's outcome must state, explicitly, which of these the findings support:

- **Options are limited** — the classification is unambiguous and there is one
  obvious minimized file. Skip the brainstorm; file a todo that applies the diff.
- **Options diverge** — sections where reasonable people would draw the line
  differently, or where "move to the guide" competes with "move to a code
  comment" or "enforce it in code instead of prose". Those need a brainstorm
  before anything is applied.

Do not file either follow-up while the audit is open, and do not assume the
second is needed. Naming which one, with the specific sections that forced the
call, IS part of the deliverable — an audit that ends without saying whether its
conclusions are settled leaves the next session to re-litigate them.

Likely candidate for the "diverges" bucket, flagged so it is not overlooked:
finding 1 (the missing commit-to-main prohibition), where the real options are
prose in the guide, a code gate, or nothing at all — and this audit's own
minimizing direction argues against the first.
