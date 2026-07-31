# E-1833 — Draft ROADMAP from a review of all tasks and decisions

Reviewed the full main-DB ledger: **1072 tasks** (519 open / non-terminal) and
**125 decisions**. Roots-with-≥3-children yield **32 epics**; open work spreads
across phases `now` (266), `next` (122), `later` (96), `maybe` (32), `urgent` (3).

This deliverable is (1) a findings summary and (2) a **draft** ROADMAP.md
grounded in what is actually filed. It does not publish — E-1835 decides what to
publish, E-1836 explores roadmap-as-a-feature.

---

## Part 1 — Findings

### Dominant themes (grounded in filed epics + accepted decisions)

1. **Event-sourced ledger as the foundation.** The single largest architectural
   through-line. E-799 (migrate to event-sourced architecture, 18 open children)
   + E-807/E-808 (projection engine, authoritative write path) + E-1671 (event
   upcasting pipeline, 8 open children) + rebuild-db safety (E-1654, E-1824).
   Decisions ratify it: ED-1352 (events-as-primitive), ED-1472 (schema.sql +
   per-ticket change files replace migrations), ED-1198 (`.endless/events/` →
   `.endless/db-ledger/`), ED-1525/1527 (ledger edit policy, tombstoning),
   ED-901/903/906 (committed per-node segments, Go single writer). *The ledger IS
   the DB's WAL — everything else is a rebuildable projection.*

2. **Python → Go DB consolidation.** "Python has zero SQLite knowledge" milestone.
   E-1486 (port every read+write, G1–G8 children), between E-894 (reads) and the
   deferred E-1063 (full port). ED-1506/1505 (Go consts are source of truth),
   ED-903. Config half: E-949 onto go-cfgstore.

3. **Parallel-session / worktree infrastructure.** The oldest large epic, E-968
   (worktrees, 29 open children) + E-971 + sandbox pattern E-995 (11 open) +
   self-dev worktree correctness (E-1667, E-1667/E-1668). Decisions ED-1168 (every
   task gets a worktree), ED-1530 (worktree.json vs .lock), ED-1521 (provisioning
   copies prebuilt binary), ED-1515 (one canonical worktree+sandbox per task).

4. **Distributed multi-developer collaboration — the newest strategic pivot.**
   E-1829, explicitly "prompted by onboarding the first external user." Short-term
   milestone: two devs collaborate via GitHub push/pull, each rebuilding SQLite
   from the shared db-ledger. Sub-threads: collision-free distributed task IDs
   (E-1831), pluggable task backends (E-800: Beads/JIRA/GitHub Issues/Notion
   mirrors, E-818–E-850). This is what turns Endless from a personal tool into a
   product — and it depends on themes 1–3 being solid.

5. **Task lifecycle, types, and the approval gate.** Canonical status workflow
   (ED-1529/1522/1513: `ready` = human-approved-to-spawn), epic as a task type
   (E-1537, 5 open children + ED-1503/1509/1511), research/brainstorm outcome
   gates (ED-1520/1507/1502/1516), and **auto-spawn** (E-1812 + ED-1538/1539:
   spawn eligibility from human-ratified complexity × risk axes).

6. **Verification as a first-class system.** E-1596 (per-task verify scripts in
   `tests/tasks/`, land-time acceptance tests per ED-1534) maturing toward a
   portable `verify.toml` spec + `tbv` Go reference impl (ED-1535/1536/1537).

7. **Agent-output discipline / reducing the user's review burden.** E-1785
   (no double-review, no narrating what the tool computes) + `task report` as a
   structured steering prompt (ED-1531/1532). Feeds directly into external-user
   readiness — an onboarding user won't tolerate ceremony.

8. **Session awareness & observability.** E-1461 (`session status`, done),
   E-857 (conversation history), E-1031 (session focus), E-697 (inter-session
   messaging), the session_* table family (E-1390–E-1394), and TUI monitors
   (E-859, E-1622).

9. **Surface / interaction direction — leaning off tmux.** E-1810/E-1809
   (decouple session launch/drive behind a pluggable multiplexer driver) plus TUI
   monitor work signals a soft strategic lean away from the tmux popup toward
   TUI/GUI surfaces.

10. **Supporting streams:** documents as first-class entities (E-801, markdown
    render E-445/E-449, decisions-as-table E-1511); curated per-project `next`
    list (E-1421, 5 open children); web/status-page UI (E-516); enforcement-gate
    consolidation (E-915, E-1712/E-1703); memory-lessons → features (E-1721).

### What looks near-term shippable (in-flight `underway`/`ready`, phase `now`)

- **First-external-user collaboration slice** (E-1829 short milestone + E-1831 ID
  scheme) — the flagship near-term deliverable driving the roadmap itself.
- **Verification-suite convention** (E-1596) — gated on a Mike interview per its
  description; high leverage once landed.
- **Review-burden reduction** (E-1785 + E-1803 report gating) — actively underway.
- **Epic task type** (E-1537) and **decisions dedicated table** (E-1511) — both
  `ready` with children queued.
- **Self-dev worktree candidate-build correctness** (E-1667) — underway.
- **Enforcement-gate consolidation** (E-1712 / E-1703) — `ready`.
- **Python→Go writes** (E-1486 G-children) — `ready`/`next`.
- **Auto-spawn eligibility** (E-1812) — architecture decided (ED-1538/1539),
  implementation pending.
- **Memory→features triage** (E-1721 / E-1724 / E-1741) — `ready`.

### Gaps — ledger implies a direction but no task cleanly captures it

- **Binary distribution / packaging for external users.** E-716 ("Package Go
  binaries for distribution") sits alone at `later`/root, but E-1829's external-user
  push makes *install-without-a-dev-toolchain* a near-term dependency. No task links
  packaging to the onboarding milestone. **Likely a real gap.**
- **Stale-`maybe` pruning.** ED-1089 explicitly deferred this ("a future task will
  consider periodic pruning"); 32 open `maybe` items now exist and no pruning task
  is filed. **Confirmed gap** (search found none).
- **Ledger hygiene / epic re-parenting.** 682 root tasks vs only 32 real epics —
  hundreds of flat roots that thematically belong under epics. No task tracks a
  structural re-parenting/organization pass. This directly undercuts roadmap
  generation (theme 10 / E-1836). **Likely a real gap.**
- **Roadmap-as-a-feature depends on ledger structure it doesn't yet have.** E-1836
  (roadmap tooling) presumes themes/horizons are derivable from the ledger, but
  phase (`now/next/later`) is the only horizon signal and epics are undersized
  relative to the flat-root sprawl. Tooling will need a theme/horizon model that
  the current schema doesn't capture. Worth surfacing to E-1836.

### Method note

Grounded strictly in filed titles, statuses, phases, parent-child structure, and
decision statements from the main DB. Horizons below map to filed `phase` +
in-flight status, not aspiration. Where a theme is inferred from a decision cluster
rather than an epic, that is stated.

---

## Part 2 — Draft ROADMAP.md

> Draft only. Themes and horizons are derived from the ledger as of 2026-07-31.
> "Near-term" = in-flight (`underway`/`ready`) or phase `now`; "Mid-term" = phase
> `next`; "Long-term" = phase `later`/`maybe` or explicitly-deferred work.

### Where Endless is headed

Endless is evolving from a single-developer project-awareness tool into a
**multi-developer, distributed collaboration substrate** for AI-agent-driven work.
Three foundations make that possible — an **event-sourced ledger** that any
contributor can rebuild, a **Go-owned data layer**, and **isolated per-task
worktrees** — and the collaboration, verification, and agent-discipline layers
build on top.

---

#### Near-term — onboarding the first external user

*The organizing goal: two developers collaborate on one project via GitHub
push/pull, each rebuilding their SQLite projection from the shared db-ledger.*

- **Distributed collaboration slice** — collision-free task IDs (E-1831),
  db-ledger rebuild robustness across contributors, first external-user onboarding
  (E-1829).
- **Verification as a system** — per-task verify scripts committed to
  `tests/tasks/`, run as land-time acceptance tests (E-1596).
- **Reduce the user's review burden** — no double-review, no narrating what the
  tool computes; structured session reports over freeform prose (E-1785).
- **Task-model maturity** — epic as a first-class type with auto-derived lifecycle
  (E-1537); decisions promoted to a dedicated table (E-1511); auto-initiated
  spawns gated on ratified complexity × risk (E-1812).
- **Self-dev correctness & enforcement** — worktrees exercise their own candidate
  build (E-1667); consolidated PreToolUse edit-enforcement gates (E-1712).
- **Data-layer port** — remove Python SQLite access, routing durable writes
  through the event log (E-1486).

#### Mid-term — the event-sourced core lands

- **Event sourcing goes authoritative** — projection engine rebuilds SQLite from
  the logs (E-807); the write path makes event logs authoritative over SQLite
  (E-808); Go owns DB creation and reads (E-894, E-1481/E-1482/E-1484) — E-799.
- **Pluggable task backends** — Beads / JIRA / GitHub Issues / Notion as
  synchronized mirrors behind a Backend interface (E-800).
- **Documents as first-class event-sourced entities** — section-level events,
  proposal/review workflow, markdown rendering (E-801, E-445/E-449).
- **Session awareness** — conversation history & recaps (E-857), session focus
  (E-1031), inter-session messaging (E-697), the session_* observability tables
  (E-1390–E-1394).
- **Curated per-project `next` list** — lanes, consistency hooks, situational
  briefing (E-1421).
- **Config on go-cfgstore** — layered defaults/user/repo config (E-949).

#### Long-term — distributed maturity & new surfaces

- **Event-upcasting pipeline** — forward-only declarative transforms so rebuild-db
  replays historical events into the current schema without rewriting the committed
  ledger (E-1671).
- **Distributed collaboration at scale** — beyond two developers: backend
  migration/switching (E-821), collision handling under short-id worktrees
  (E-1123).
- **Surface evolution** — decouple session launch/drive from tmux behind a
  pluggable multiplexer driver (E-1810); TUI agent monitors with live refresh
  (E-1622, E-859).
- **Binary distribution** — package Go binaries so non-developers can install
  (E-716) — *note: candidate to pull near-term given external-user onboarding.*
- **Roadmap as a product capability** — generate/maintain roadmaps from the ledger
  (E-1836).

---

*Backlog note:* the ledger holds ~519 open tasks; the horizons above surface the
epics and in-flight work that define direction, not every filed item. Many smaller
tasks live as flat roots outside these epics (see Findings → Gaps).
