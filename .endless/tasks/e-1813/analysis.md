# What `tasks.tier` is today

Surveyed by ES-1102, revised in E-1815 after the questions this was gathered to
answer were settled in conversation. File references name symbols rather than
line numbers, which go stale on every land.

## Shape

- `tasks.tier` is a bare nullable `INTEGER` on `tasks` (`internal/schema/schema.sql`).
  No FK, no values table — it predates the enum-as-integer-FK convention the
  ratings work (ED-1538) follows.
- Its labels exist only in Python: `0=n/a, 1=auto, 2=quick, 3=deep, 4=discuss`
  (`_TIER_LABELS`, `src/endless/task_cmd.py`). The Go side reads the raw int.

## Actual use in the real ledger

| tier | rows |
|---|---|
| NULL | 187 |
| 0 (n/a) | 1052 |
| 1 (auto) | 12 — all `ready` |
| 2 (quick) | 4 |
| 3 (deep) | 4 |
| 4 (discuss) | 8 |

28 meaningfully-rated rows out of ~1250. The field is, in practice, unused.

## Why it is invisible

`task show` renders the Tier line under `if item["tier"]:` (`task_cmd.py`) —
falsy for **both** NULL and 0. With 1239 of 1267 rows at NULL-or-0, the line is
absent for ~98% of tasks. That is the reported "does not even show up in
`task show`": not a missing renderer, a guard the data almost never clears.
E-1233 already owns adding the line properly.

## What tier still drives

- `--tier 1` auto-advances a pre-work task (`untriaged`/`unplanned`) to `ready`
  and exempts it from triage (`task_cmd.py`).
- `task next --tier` filter; the `task list` Tier column and its sort key.
- The tmux status line and the session-notice headline
  (`internal/monitor/session_notices.go`, `internal/monitor/tmux_lookup.go`).
- The task change-tracking trigger (`internal/schema/schema.sql`).
- Documented in `docs/guide/tasks.md` (the `--tier` flag rows and the
  `--keep-status` auto-transition table).

## Resolution (E-1815)

**Complexity replaces tier.** Tier's ladder — auto → quick → deep → discuss — is
a human-interaction-depth scale, and ED-1539 defines complexity as "how much
human-AI interaction is needed to nail down the specifics." Same judgment, two
names. Tier's only behavior, the `--tier 1` auto-`ready`, is something complexity
could carry identically, so tier has no behavior complexity cannot absorb and no
meaning it does not duplicate.

**Do not reverse-engineer tier's intent.** It predates current thinking, is ~2%
populated, and is entangled with the shelved background-tasks work. Design
complexity and risk from ED-1538 and ED-1539 directly. Tier is a column to
remove, not a design to reconcile with.

**Do not carry `--tier 1`'s auto-`ready` forward.** A rating value does not move
status; status routing is triage plus approve, consistent with E-1844 ("with
status — not complexity/risk — routing the workflow"). E-1153, which was specced
as shorthand for `--tier 1 --phase now --status ready`, is obsoleted, so nothing
depends on the shortcut.

**Do not spend effort on the "gate collision."** An earlier revision of this
analysis framed `--tier 1` reaching `ready` without approval as a hole in
ED-1538's approval gate. Judged in E-1815 as overstated: work happens in
worktrees and there is no auto-land, so an agent reaching `ready` on its own has
no path to harm short of going haywire, at which point there are larger problems.
Recorded so the thread is not reopened.

**Migration cost is low.** The 1052 tier-0 rows mean "n/a" — nothing to migrate.
28 rows carry meaning. The remainder is mechanical code surface: CLI
parse/filter/display, the Go monitor readers, the schema change-trigger, and the
guide docs.

## One open question worth carrying

Tier reached 28 meaningfully-rated rows out of ~1250. Whatever suppressed its
adoption may suppress complexity's too. Two candidates, both addressed by the
design below but worth confirming rather than assuming: nothing ever *required* a
tier (agent-proposes-at-submit does require a rating), and the value was
invisible in `task show` for ~98% of tasks (E-1233 fixes the render guard).

## Not this

`src/endless/models.py` carries an unrelated `tier: int = 5` on the
project-discovery model. Same word, different concept — do not conflate.
