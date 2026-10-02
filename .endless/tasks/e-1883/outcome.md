# E-1883 synthesis — scoping Endless databases

Brainstorm with Mike, 2026-10-01. Decision: ED-1602. Epic: E-2215.

## Verdict: split is needed

Two drivers were weighed.

- **Project-owned state.** Already met by the committed db-ledger: the durable
  record lives in each project's repo; the database is a projection. This
  driver alone did not force a split. Whether anything project-owned is
  missing from the ledger is deferred — the split forces every table into a
  machine or project file, so the inventory happens in E-2215's plan, and any
  gap found later is "emit events for mutations the projection must rebuild"
  work, not a scoping problem.
- **Rebuild safety.** This is what forces the split. Keeping one database
  means either:
  - a surgical project-scoped rebuild, which needs a hand-maintained
    classification of every table and field as project- vs machine-scoped —
    a maintenance burden that corrupts data the first time it is wrong, and a
    larger robustness-testing surface; or
  - rebuilding every registered project's ledger on each rebuild — too slow,
    likely a deal-killer on its own.
  With separate files the classification is structural (which file a table is
  in) and rebuild is "delete one project's file, replay one ledger."

The cost of a real split (every query, the `--db` seam, cross-project views,
migrations to N files) is accepted as the price.

## Shape

- **Machine DB** `~/.config/endless/endless.db`: sessions, panes, processes,
  worktree locks, jobs, errors.
- **Project DB** `~/.config/endless/projects/<slug>.db`. Considered
  `<project>/.endless/<project>.db` (gitignored; easy to find from an IDE) but
  chose the config dir: the database should not be edited directly, one file
  per project regardless of worktrees, and it keeps the single ConfigDir()
  seam that the sandbox and `--db main|sandbox` depend on. "Main" = the
  machine DB plus every project DB under ConfigDir(); the sandbox mirrors
  that layout.
- **Cross-file references: per-kind mirror tables, not one registry.** A
  single generic registry (global id, project, kind) was proposed and
  rejected: mirrors like `main.tasks` can grow synced content fields (title,
  description) as needs arise; a single registry cannot. Machine tables FK to
  the mirrors, so SQLite still enforces them.
- **Mirror sync by an idempotent reconcile job**, plus a refresh of a
  project's mirror rows when it is rebuilt. Writing both files in one
  transaction is not safe: SQLite does not guarantee atomicity across
  attached databases in WAL mode.
- **Cross-project reads via ATTACH.** A developer is unlikely to have more
  than 9 active projects (default attach limit 10). A per-project opt-out is
  filed for later (E-2218).

## Rejected

- Surgical rebuild inside one database (option 2) — classification burden,
  corruption risk.
- `pNNN_` / `m_` table prefixes in one file (WordPress-multisite style) — the
  same classification burden, without the clean one-file rebuild.
- Document the limit (option 5) — does not meet rebuild safety.
- One generic registry table for cross-file FKs — see mirrors above.

## Issues surfaced along the way

- **Event attribution (E-2216).** An event's `project` comes from the caller's
  cwd, not from the entity changed (`task update E-1629` from endless filed a
  go-tealeaves change into endless' ledger). `--project` is not the fix: an
  entity-addressed command should not need it. Required under every option;
  mandatory under the split, where an id must route to its project DB first.
- **Unique project slugs (E-2217, blocks E-2215).** Endless has silently
  relied on project slugs being unique on a machine. Two clones or forks of
  one repo registered side by side would share a project DB file. Needs its
  own brainstorm before the split lands.

## IDs — deferred to E-799

- Short ordinal IDs would ideally be local handles, but that is not
  realistic: an agent will not reliably translate a local handle to the
  global ID before writing it into the ledger. So the stored identity is
  the globally unique ID; ordinal numbers, for those who want them, would
  come from a globally reachable ID service (self-hosted, a freemium
  service, or Jira/GitHub Issues integration).
- Short IDs must be memorable short-term (tmux tabs, conversation), which
  rules out git-style short-hash prefixes.
- E-799's children already hold a design; specifics wait until E-799 is
  implemented. The split does not block on it: today's machine-wide ordinal
  sequence keeps mirror keys unique on one machine.

## Follow-ups filed

- ED-1602 — decision: machine DB + per-project DB, mirrors, ATTACH (proposed).
- E-2215 — epic: implement the split (plan lists the children to file:
  table inventory, mirrors + reconcile job, `--db`/sandbox resolution,
  cross-project reads, goose migrations to N files, one-time migration).
- E-2216 — bugfix: derive an event's project from its entity, not cwd.
- E-2217 — brainstorm: guarantee unique project slugs per machine; blocks
  E-2215.
- E-2218 — later, under E-2215: per-project opt-out of ATTACH.
