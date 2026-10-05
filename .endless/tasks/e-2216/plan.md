# Derive an event's project from its entity

Scoped with Mike, 2026-10-05. Cross-project link storage by visibility was
split out to E-2247, which this task blocks.

## 1. Attribution at the single emit point

- Every event about an EXISTING task or decision takes its `project` from
  that entity, resolved where events are emitted (`endless-go event emit`),
  not from the caller's cwd — one enforcement point for the CLI, the web and
  any future path.
- An explicit `--project` that disagrees with the entity's project is
  refused, naming both, rather than silently picking one.
- Creation events (`task add`, `decision add`) have no entity yet: they keep
  using `--project`, else cwd.
- Ledger commit routing follows the stamped project, so the git commit lands
  in the entity's repo.

## 2. One lookup function

- The entity → project lookup lives behind one Go function. Today it queries
  the single database; under the storage split it switches to the machine
  database's mirror tables, a change confined to that function.

## 3. Repair history

- Scan every registered project's ledger for events whose stamped project
  differs from their entity's project (E-1629's update in endless' ledger is
  the known case).
- Re-attribute them with corrective events — the ledger is append-only,
  never edit or delete lines. Reuse whatever corrective-event mechanism the
  session-misattribution repair uses, if it exists.
- This must be done before the storage split migrates today's database:
  replaying a misattributed event into a per-project database names a task
  that file does not contain.

## Verification

- From the endless checkout, `endless task update <a go-tealeaves task>
  --phase later`: the event is stamped go-tealeaves and committed in
  go-tealeaves' repo; endless' ledger is unchanged.
- The same command with `--project endless` is refused, naming both projects.
- `task add` from the endless checkout with no `--project` still files into
  endless.
- After the repair, the scan reports no events whose project differs from
  their entity's.
