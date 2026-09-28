# E-1813 — complexity and risk axes replace `tasks.tier`

Implements ED-1538 and ED-1539. **Both are currently `proposed`, not accepted** —
they were reset in E-1815 after being found malformed (long titles, no
descriptions) and self-accepted by the filing session. Do not start until they
are ratified; if either is rejected or amended, this spec changes with it.

The survey of what `tasks.tier` is today, and the reasoning for replacing it
rather than coexisting with it, is in this task's analysis field. Read that
first — it also records which threads were closed in E-1815 so they are not
reopened.

## 1. Data model

Two nullable integer FK columns on `tasks` pointing at two seeded values tables,
`complexity_levels` and `risk_levels`. Seed `low=1`, `medium=3`, `high=5`, with
2 and 4 deliberately unseeded so medium-low and medium-high can be added later
without renumbering.

Go enums follow the house pattern: int-const, `String()` returning the slug, and
`Parse()`. Source of truth is the Go const; the values table mirrors it.

## 2. Propose and ratify

The agent proposes both ratings at submit. The user ratifies them at approve,
reusing the existing approval gate rather than adding a second one (ED-1538).

Ratings do **not** move status. Status routing remains triage plus approve,
consistent with E-1844. In particular, do not reproduce `--tier 1`'s behavior of
auto-advancing a pre-work task to `ready`.

## 3. Remove `tasks.tier`

Drop the column and everything reading it:

- CLI: the `--tier` flag on `task add`/`task update`, `parse_tier`,
  `parse_tier_filter`, `tier_display`, `_TIER_LABELS`, the `task next --tier`
  filter, and the `task list` Tier column and sort key.
- Go: the tmux status line and session-notice headline readers.
- Schema: the column and its change-tracking trigger entry.
- Docs: the `--tier` rows in `docs/guide/tasks.md` and the `--keep-status`
  auto-transition table entry.

Data migration is trivial — 1052 rows at tier 0 mean "n/a" and 187 are NULL. The
28 meaningfully-rated rows are the only judgment call: map them onto complexity
or drop them, implementer's call, but say which in the outcome.

E-1233 (add the Tier line to `task show`) is superseded by this and should be
closed out when this lands — the display it describes is for a column that will
no longer exist. E-1153 is already obsoleted.

## 4. Out of scope

- The eligibility predicate and the selector (E-1814).
- Thresholds beyond the initial hardcoded low/low (E-1814).
- Any auto-spawn runtime concern — window, throttle, cap (E-1815, folded into
  E-1814).



## 5. Settled with Mike during implementation (ES-1248)

The plan left the propose/ratify mechanics open; these were decided in session:

- **`task submit` requires both ratings.** `--complexity` / `--risk` on the call,
  or already on the task; refused otherwise, naming the flags.
- **The triager proposes ratings** when it routes a task to `submitted`: the
  sufficiency template asks for `COMPLEXITY:` / `RISK:` lines, and the parsed
  values are emitted as a `task.fields_updated` by actor `triager`. Missing or
  malformed ratings do NOT fail the routing (fail-open stays fail-open) — the
  task is simply unrated and approve catches it.
- **Plan-attach auto-promote does not require ratings** (it is a system
  inference, not an explicit submit); it prints a one-line nudge naming
  `--complexity` / `--risk` when the promoted task is unrated.
- **`task approve` refuses an unrated task.** It accepts `--complexity` /
  `--risk` to supply or override, and reports the ratings it approved.
- **Claiming/spawning never requires ratings.**
- **Ratings are freely editable** via `task add/update --complexity/--risk`
  at any status; `none` clears. No status moves on a rating edit.
- The 28 meaningfully-tiered rows are **dropped**, not mapped: a mapped value
  would read as a ratified rating nobody ratified.

## 6. Grown scope, folded in during implementation

- `task update --status submitted` / `--status ready` meet the same rating
  gates as `task submit` / `task approve` — the same acts under another verb.
  Gated only on the edge each verb accepts; from anywhere else the lifecycle
  guard's refusal is the one shown.
- The transition table's tier-1 exemption edges (untriaged/unplanned → ready)
  are removed, and the generated diagram copies regenerated.
- `src/endless/db.py`'s connect-time `_migrate_v2` re-added `tasks.tier` and
  updated `tier = 1` rows; both removed, or they would undo migration 00008.
- `task clear tier` became `task clear complexity` / `task clear risk`.
- Status changes no longer zero tier in the executor/projector; the ledger
  validator compares `complexity_id` / `risk_id` instead.
- `task show` always renders the ratings, `unrated` included (human:
  `Ratings:` line; `--agent`: on the status line where `tier=` sat).
- Epic commands lose `--tier` and gain no rating flags; ratings on an epic go
  through `task update` / `task approve`.

E-1233 is superseded by this and should be closed when it lands.
