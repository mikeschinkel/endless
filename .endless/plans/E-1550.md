# Add active-blocker segment to tmux pane status line

## Goal

Surface the active task's blocker chain on the endless tmux status line
so Mike can tell at a glance whether the current task is unblocked or
waiting on something — without having to run `endless task show` or
keep a manual nano buffer in a sibling pane.

## Mockup

Status line for a task with two active blockers and at least one more:

```
[E-1434] - endless - task - next - verify - {E-1549 E-1550 +}
```

Status line for an unblocked task:

```
[E-1434] - endless - task - next - verify
```

(No trailing segment, no separator — silence = clear.)

## Decisions baked in

1. **Filter to active blockers only.** A blocker counts as active when
   its status is NOT in `{confirmed, assumed, declined, obsolete}`. As
   soon as a blocker becomes terminal, it drops off the segment — no
   manual cleanup. When the last active blocker drops off, the segment
   disappears entirely.
2. **Cap inline at 2 IDs, then `+` overflow.** `{E-1549 E-1550 +}` for
   3+ blockers; `{E-1549 E-1550}` for exactly 2; `{E-1549}` for 1.
3. **No inline label.** Color + delimiter shape carry the meaning.
   Adding `Blocked by:` / `Needs:` etc. eats columns to repeat what
   the color and the segment's presence already communicate.
4. **Color the whole segment (braces + IDs).** Red/orange while any
   active blocker exists. The shape and color are the signal; absence
   of color is impossible because the segment vanishes when there are
   none.
5. **No status glyph per blocker.** Mike asked for this out of scope;
   `task show` covers the deeper view.
6. **No reverse direction.** Showing tasks that THIS task blocks is
   out of scope; `task show` covers it.
7. **No menu integration.** Click/keystroke menu wiring is out of
   scope; the segment is glance-only.

## Implementation sketch

- Status-line rendering lives under `internal/tmuxcmd/`. The existing
  field set is dash-separated (`[id] - project - type - phase - status`)
  and is composed in `status_line.go` and applied in `apply.go`.
- Add a `blockers` segment renderer in `status_line.go` that:
  - Resolves the current task ID from the pane context (existing path
    used by the other segments).
  - Runs one join: `task_deps` (where `target_id = current_task AND
    dep_type='blocks'`) → `tasks` (id, status), filter on active
    statuses, order by id ASC.
  - Renders `{E-NNNN E-MMMM[ +]}` with up to 2 inline IDs.
  - Returns empty string when zero active blockers (renderer composes
    the leading ` - ` only if the segment is non-empty).
- Wrap the rendered segment in tmux color escape codes (red/orange).
  Check what existing segments use — match that mechanism (likely
  `#[fg=...]…#[default]` style).
- Cheap path: pure DB read; called per status-line refresh (already
  rate-limited by tmux's own status interval).

## Files changed

- `internal/tmuxcmd/status_line.go` — new segment renderer + composition.
- `internal/tmuxcmd/status_line_test.go` — cover the test plan below.
- Possibly `internal/tmuxcmd/apply.go` if the segment needs wiring
  through the apply path; inspect at implementation time.

## Test plan

1. **No active blockers** → renderer returns empty string; status line
   has no trailing segment, no trailing ` - `.
2. **One active blocker** → `{E-NNNN}`.
3. **Two active blockers** → `{E-NNNN E-MMMM}` (alphanumeric / id-sort
   order, stable across refreshes).
4. **Three+ active blockers** → `{E-NNNN E-MMMM +}`.
5. **All blockers in terminal status** (confirmed / assumed / declined
   / obsolete) → no segment; same as case (1).
6. **Mixed: some active, some terminal** → only the active ones appear,
   counted toward the cap.
7. **Color rendering** → segment characters wrapped in the expected
   tmux color escape; segment is dropped (not just hidden) when empty,
   so no stray escape codes remain.
8. **No current task** (no `[E-NNNN]` in pane context) → no blocker
   segment, no error.

Tests build their own task fixtures using the existing test helpers in
the package; the nearest template is whichever existing `*_test.go`
already exercises status-line composition.

## Verification (manual)

1. Open a tmux pane whose session is bound to E-1434. With this fix
   landed, the status line shows
   `[E-1434] - endless - task - next - verify - {E-1549}` (colored).
2. `endless task confirm E-1549` (or whatever's blocking) → next status
   refresh, the segment vanishes.
3. Block E-1434 by a fresh `endless task add` + `endless task block` →
   segment reappears with the new ID.

## Out of scope (file as separate follow-ups when relevant)

- Per-blocker status glyph (ready / in_progress / verify).
- Reverse direction segment (`blocks: E-NNNN`).
- Tmux menu / keystroke integration for full blocker list.
- Color-theming the segment based on aggregate blocker status
  (e.g., orange if all blockers are in_progress, red if any need_plan).
