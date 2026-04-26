# 3. What it is not
- A memory system like Beads
- A knowledge tracking Wiki 

## Endless vs. Beads 

| Feature                                  | Endless                                                                                                   | Beads                                                                                                 |
|------------------------------------------|-----------------------------------------------------------------------------------------------------------|-------------------------------------------------------------------------------------------------------|
| **Portfolio view (across repos)**        | Yes, native — single DB aggregates all projects                                                           | No — per-repo by design [1]                                                                           |
| **Session awareness**                    | Yes — `sessions` table tracks platform, state, `active_task_id`                                           | No first-class session model [2]                                                                      |
| **Correctness enforcement (write-time)** | Yes — `PreToolUse` can block Write/Edit without declared task                                             | No — workflow enforced by AGENTS.md prose [3]                                                         |
| **Correctness enforcement (close-time)** | Not yet — architecturally straightforward to add                                                          | Yes — `bd close` checks gates before allowing close                                                   |
| **Coordination primitive**               | Channel/beacon/send messaging [4]                                                                         | Atomic `--claim` flag [5]                                                                             |
| **Dependency types**                     | 1 in practice (`needs`, unused); schema reserves `blocks` [6]                                             | 5 core (`blocks`, `parent-child`, `related`, `discovered-from`, `conditional-blocks`) + 4 graph links |
| **Gates (external conditions)**          | None                                                                                                      | `gate` issue type with `await-type` of PR/run/timer/bead/human                                        |
| **Statuses**                             | 7: `needs_plan`, `ready`, `in_progress`, `verify`, `completed`, `blocked`, `revisit`                      | 5+1: `open`, `in_progress`, `blocked`, `deferred`, `closed`, `tombstone` [7]                          |
| **Human-in-loop verification**           | Yes — `verify` status separates "AI says done" from "human confirmed"                                     | No — agent's `close` is ground truth                                                                  |
| **Planning horizon**                     | `now` / `next` / `later` phases as first-class field                                                      | No phases; uses `priority` 0–4                                                                        |
| **Task identity**                        | Integer (`E-47`) — human-typable                                                                          | Hash (`bd-a3f2`, scales to 6 chars) with hierarchical extension [8]                                   |
| **Storage topology**                     | Centralized SQLite in `~/.config/endless/`                                                                | Per-repo Dolt in `.beads/` + JSONL export committed to git                                            |
| **Multi-agent concurrency**              | Single writer to central DB                                                                               | Cell-level auto-merge via Dolt [9]                                                                    |
| **Repo-contained state**                 | No today — DB lives outside any project                                                                   | Yes — `.beads/` clones with repo; state survives fresh machine                                        |
| **Hook coverage (Claude Code)**          | Planned: 6 events (`SessionStart`, `PreToolUse`, `PostToolUse`, `UserPromptSubmit`, `Stop`, `SessionEnd`) | 2 events (`SessionStart`, `PreCompact`)                                                               |
| **File-change ground truth**             | Yes — `PostToolUse` captures every Write/Edit with session attribution                                    | No — only what the agent self-reports via `bd`                                                        |
| **First-party UI**                       | Local web dashboard at `:8484`, peer with the data store                                                  | None; community UIs (bdui, Lista Beads, etc.) are third-party                                         |
| **Cross-project dependencies**           | Schema supports it (polymorphic `source_type`/`target_type`); not yet used                                | Architecturally forbidden [1]                                                                         |
| **Distribution**                         | Single Go binary (Python is prototype only)                                                               | Single Go binary via brew, npm, `go install`                                                          |
| **Community & reach**                    | Pre-release                                                                                               | ~19.5k stars, plugin ecosystem, Yegge's audience                                                      |

---

## Footnotes

**[1] Per-repo isolation.** Beads's FAQ states that issues cannot reference issues in other projects and that each database is isolated by design. The architectural reason is the "clone this repo and get complete state" invariant — cross-repo references would require a registry living outside any single repo, breaking clone-completeness. Endless maintains cross-project dependencies in the central portfolio DB (and, under the planned event-sourcing model, in the changelog repo).

**[2] Session model.** Beads models assignees as free-form strings. Gas Town, Yegge's separate agent-orchestration layer, models agents as beads but that is outside Beads core. Endless's `sessions` table is first-class and underpins portfolio-level "what is every agent doing now" queries.

**[3] Enforcement model.** Endless enforces *intent at write time* via `PreToolUse`, which can refuse a Write/Edit when no task is declared. Beads enforces *evidence at close time* via `bd close` gates. The models are complementary; a tool with both would have neither's weakness. Endless could add close-time gates with modest effort.

**[4] Messaging.** Endless's channel primitive (beacon/connect/send) is decoupled from the sessions table. It is a multi-agent realtime communication primitive. It is not a coordination claim, and it has not been validated as a commercial wedge.

**[5] Atomic claim.** Beads's `bd update --claim` atomically sets assignee and `in_progress` in one transaction, preventing two agents from racing for the same issue. Endless has no equivalent today; adding one is on the architectural-debt list.

**[6] Dependencies in practice.** Endless's `task_deps` table currently holds one record with `dep_type = 'needs'`, and no dependency logic drives behavior. The `blocks`/`needs` distinction in the schema is vestigial — unvalidated design space reserved before the feature was implemented. The honest framing is that Endless has no dependency system today.

**[7] Status design.** Beads's statuses are tight and oriented around agent-close-then-audit-later workflows. Endless's seven reflect an explicit commitment to keeping the human in the loop at both design time (`needs_plan`, `revisit`) and verification time (`verify`). This is a durable differentiator for the human-in-loop workflow and cannot be matched by adding one enum value to Beads, because the architectural commitment to agent-autonomy in Beads would make `verify` semantically incoherent there.

**[8] Identifiers.** Beads's IDs start at 4 characters and auto-scale to 6 as the DB grows, with hierarchical extension for parent-child (`bd-a3f2.1.1`). This is type-heavy relative to `E-47` but collision-resistant for multi-agent concurrent issue creation. The right answer for Endless is likely both: integer for human-typable local reference, hash for cross-repo and cross-merge identity, mapped 1:1.

**[9] Cell-level merge.** Dolt stores tables as prolly trees (Merkle-DAG variants) and three-way-merges at cell granularity, so two branches editing different fields of the same row auto-merge. Dolt validates schema types, CHECK constraints, and foreign keys during merge, but does not guarantee cross-field domain invariants that aren't expressible as CHECK constraints — that class of corruption is left to the application. SQLite's binary format precludes git-native merge entirely. Endless's planned event-sourced JSONL approach avoids both the SQLite-in-git problem and the Dolt dependency while getting deterministic projection that can enforce cross-field invariants during replay.
