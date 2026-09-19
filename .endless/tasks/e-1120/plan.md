# E-1120: Show '<label>: <old> -> <new>' on all state-mutating task commands

## Problem

State-mutating task commands print confirmation messages that hide what actually changed. Two shapes of the same problem:

**`task confirm`** — shows only "Confirmed: <title>" with no prior status. If you confirm by mistake, you have no way to know what status to revert to.

```
$ endless task confirm E-995
• Confirmed: Design sandbox/fixture pattern for tests that mutate state
```

**`task update`** — shows "Updated E-###: status" listing field *names* only, not the values that changed. You can see *what* fields moved but not *how*.

## Desired output

Every state-mutating command should render changed fields as `<label>: <old_value> -> <new_value>`.

```
$ endless task confirm E-995
• E-995: Design sandbox/fixture pattern for tests that mutate state
Status: ready -> confirmed
```

```
$ endless task update E-1120 --status in_progress --phase next
• E-1120: Show '<label>: <old> -> <new>' on all state-mutating task commands
Status: needs_plan -> in_progress
Phase: now -> next
```

## Scope

Audit and fix all task subcommands that change persisted state. Known list:

- `task confirm` — currently only prints title
- `task update` — currently lists field names only (E-973's original target; source: `src/endless/task_cmd.py:1528`)
- `task start` — verify
- `task add` (when it sets a status implicitly) — verify
- Any other subcommand that writes to the tasks table

## Implementation notes

- The `update` path already has the new values in the fields dict; needs the *old* values too. Read the row before the update (or return them from the SQL layer) so the output can render both sides.
- Truncate long blobs (`text`, `prompt`, `description`) to a short preview or render as `<set>` / `<cleared>` rather than dumping the whole value.
- Render `None` as `cleared` or `∅` (e.g., for `parent_id`, `tier` clears).
- Field labels should match the CLI flag (e.g., `--status` → `Status:`, `--phase` → `Phase:`).

## Replaces

- **E-973** — "Show updated field values in task update output" (chore, ready). Same intent, narrower scope (only `task update`, only new values). Mark obsolete and point at this task.
