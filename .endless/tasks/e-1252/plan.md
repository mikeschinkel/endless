# E-1252 — Rename task statuses (atomic: code + data + docs) — PLAN

Decided 2026-06-24 (see ED-1513). This task does the **rename only**, atomically.
The values table + FK + dropping `blocked` are E-1532; the approval-gate behavior
is E-1648.

## Decided renames
| old | new |
|---|---|
| `needs_plan` | `unplanned` |
| `in_progress` | `underway` |
| `verify` | `unverified` |

Plus **add `unapproved`** to the valid set (the gate's review state — no rows
carry it yet; E-1648 starts producing them). **Leave `blocked` untouched** — it is
dropped in E-1532, not here. `ready`/`confirmed`/`assumed`/`completed`/`revisit`/
`declined`/`obsolete` unchanged.

## Atomic — do all three together (or the system is inconsistent)
`tasks.status` is currently free TEXT, so this needs no values table / FK / rebuild
— just rename consistently across code, data, and docs **in one landing**:

1. **Code** — every literal/branch on the old slugs:
   - `cli.py`: `TASK_STATUSES`, `click.Choice`/`MultiChoice`, help strings.
   - `task_cmd.py`: the `valid` tuple, comparisons (`== 'needs_plan'`,
     `== 'in_progress'`, `== 'verify'`), and the `task active`/`next`/`show`
     filters that hardcode these.
   - Go: `internal/monitor`, `internal/events`, `internal/tmuxcmd`, web — any
     status string literals.
   Add `unapproved` to the valid set in the same edit.
2. **Data** — a one-time `.sql` change-file (pure DML, no FK work):
   `UPDATE tasks SET status='unplanned' WHERE status='needs_plan';`
   (and `underway`←`in_progress`, `unverified`←`verify`). One-time ledger hygiene.
3. **Docs (Mike's call-out)** — the *names* wherever they appear:
   `endless guide` (status table + happy path), **CLAUDE.md** (project + global),
   **README.md**, **handoff templates** (`docs/templates/handoff*` + per-type
   variants), `agent_help`/guide cross-reference, web UI labels.

Re-grep for the three old slugs at implementation time; the lists above are the
known surface, not a completeness guarantee.

## Hands off (other tasks own these)
- **E-1532**: the `task_statuses`/`task_phases` values tables, the slug-FK, and
  dropping `blocked` (seed-minus-blocked + the 9 blocked-row cleanup). It seeds the
  already-renamed names — so E-1252 lands first.
- **E-1648**: the *workflow-prose* doc edits (`ready` = approved, the `approve`
  step) — coordinate so the two doc sweeps don't clobber the same guide/handoff
  sections.

## Verify
- `grep -r` finds zero `needs_plan`/`in_progress`/`verify` status literals in code
  or docs after the sweep.
- `SELECT DISTINCT status FROM tasks` shows the new names, none of the old.
- CLI round-trips: `task update <id> --status unplanned|underway|unverified` works;
  `task active`/`next`/`list --status` filters behave.
