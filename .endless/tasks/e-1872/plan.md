# E-1872 — Document the guide's memory-only conventions in one pass

Consolidates E-1873, E-1874, E-1875 and E-1885 (all obsoleted into this task)
and adds the task-proliferation rule.

Items 1–5 are each a convention a session is expected to follow that appears
nowhere in `endless guide`, so it silently breaks for a fresh session — the same
class of gap E-1870 fixed for the commit step. Item 6 is the inverse: a claim the
guide *does* make about `land` that is false. All six are edits to `docs/guide/`,
so they are **one** review, not six.

## The gaps

### 1. Commit-message convention

Branch commits ride into `main` via `worktree land`, and git history's de-facto
form is `E-<id>: <verb-first summary>` (e.g. `b6dd933e`, `fd28a75d`). Nothing in
the guide states it, so every session invents its own subject line.

**Where:** `orchestration.md`, in the "Committing your work" section E-1870 added.

### 2. Do not land or drop without asking

Every handoff template carries "Don't run `endless worktree land`/`drop` without
asking." The guide's "Landing the work" section says the opposite by omission:
"When the task is verified (or you're using `assume`): `endless worktree land
<id>`." The only trace in the guide is `orchestration.md:284` — a sentence
*describing what the handoff template carries*, not a rule a session reading the
Landing section would ever see. A session that re-reads the guide after a context
compaction will land unasked.

**Where:** `orchestration.md`, "Landing the work" — as a rule, not a description
of a template.

### 3. File-don't-fix for drive-by discoveries (`--cleans-up`)

Exists only as `orchestration.md:284` and a bare flag mention in `tasks.md:85`'s
comment block. There is no prose rule, so a session working from the guide alone
fixes drive-bys inline and inflates the diff.

**Where:** `tasks.md`, alongside the relation flags.

### 4. `FULL STATUS` escape hatch

Lives only in `internal/templatecmd/templates/handoff/_close.tmpl`: the user
typing `FULL STATUS` licenses **one** unconstrained response, not a sticky mode.
Absent from `docs/guide/` entirely, so a session whose context is compacted
either ignores the keyword or misreads it as a mode switch.

**Where:** `tasks.md`, alongside "Reporting to your user".

### 5. Lean toward FEWER tasks — user directive, 2026-08-04

When filing discovered work, prefer **one** task over several whenever one is
reasonable.

Every filed task costs the user review, prioritization, and scheduling attention.
Five tasks cost roughly **3x** the attention of one task covering the same
ground. Endless exists to **save** the user time, not to manufacture busy work —
for this user or for any downstream Endless user.

Split into separate tasks only when the items genuinely need different reviewers,
different decisions, or different land timing. Same file + same kind of edit +
same reviewer = one task.

This session's own E-1872..E-1875 — four near-identical "document X in the guide"
items — is the anti-pattern. Folding them into this one task is the pattern.

**Where:** `tasks.md`, in the section covering `task add` / filing discovered
work. This one is load-bearing enough to state as a rule with the cost rationale
attached, not a passing aside — an agent that does not know *why* will re-split.

### 6. `land` does not remove the worktree (absorbed from E-1885)

`orchestration.md`'s "Landing the work" step 4 says `land` "Removes the
worktree." It never has: `land_worktree()` records the landing and leaves the
directory and branch in place, and the E-1337 reaper deletes them once they pass
`worktree_ttl` (`.endless/config.json`, default 14d). `index.md` already
described the true behavior, so the two guide pages contradicted each other —
and the false one sits in the same step-list a session reads before landing.

Retention is also what makes re-landing a follow-up commit work, which the
corrected step should say.

**Where:** `orchestration.md`, "Landing the work" — the same section item 2
edits, hence the same review and the same land.

Filed separately as E-1885 during implementation, then folded back in here on the
user's instruction: same file, same section, same reviewer — the rule item 5
documents. E-1885 is `obsolete`, replaced by this task.

## Verification

`tests/tasks/e-1872-verify.sh` — for each of the six, assert the rule is present
in the rendered `endless guide <section>` output (not merely on disk), and that
the `land`-without-asking rule sits inside the "Landing the work" section rather
than in the handoff-template description. For item 6, assert the section carries
the retention wording and no longer carries "Removes the worktree.", and pin both
to the source they describe (`worktree_cmd.py`'s "Worktree dir and branch stay",
`cli.py`'s "default 14d"). Fold in `tests/test_guide_map.py` and `just
guide-check` as the fail-fast first check.


