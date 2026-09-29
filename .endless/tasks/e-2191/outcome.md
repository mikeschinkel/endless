# Ready backlog ratings (E-2191)

37 tasks were `ready` in `endless`. E-1814 was already rated medium/medium and
was left alone. The other 36 were each assessed by their own subagent (full
task, parent/siblings, linked tasks, the named code, and a check of `main`), and
the ratings are now written with `--keep-status`. No task's status changed.

**Result:** 1 task is low/low (E-2181). 11 are done, obsolete or superseded and
should be closed (Mike's call). 7 are "unsure" and need a keep/re-scope
decision. The remaining 17 are worth doing but not auto-spawnable.

**Rule applied beyond the plan:** a task judged already done, obsolete or
superseded got complexity ≥ medium, even when the leftover work is trivial on
its own terms. Otherwise E-1132, E-1133 and E-1098 (all phase `now`) would have
come out low/low and auto-spawned agents on work that doesn't need doing.
Their "complexity" is really "a human must decide to close or re-scope".

**Cross-cutting caveats**
- **Stale plans.** About ten plans predate status renames (needs_plan,
  in_progress, blocked), the web-surface removal (E-1939), tier→ratings
  (E-1813) or schema changes (E-2108). A stale plan pushed complexity up
  wherever it applied.
- **E-1063 says** "once the port starts, no new Python work lands in
  task_cmd/cli/worktree_cmd/session_cmd". The port has not started (E-1063
  is still blocked by E-1486, E-1921 and E-2020, all submitted), so Python-side
  tasks are still landable. When the port does start, re-check every
  Python-touching row below, including E-2181.

## 1. Low/low — auto-spawnable once E-1814 is enabled

| ID | Title | Worth doing | Plan | C | R | Reasons |
|---|---|---|---|---|---|---|
| E-2181 | Name a spawned session after its task | yes | yes | low | low | C: one-line default (`name = f"e-{id}"` when none given) with exact acceptance criteria and a named test file; the pass-through (`spawn_plan` in task_cmd.py, `spec.Name` in spawn_launch.go) exists and is tested. R: only the claude `--name` argument; nothing in Endless reads it back; an explicit `--name` still wins; one-line revert. |

## 2. Worth doing, not auto-spawnable

| ID | Title | Plan | C | R | Reasons |
|---|---|---|---|---|---|
| E-1230 | Add 'task links' alias, drop 'task relations' | yes (file list stale) | low | medium | C: decisions made; the plan misses tests/test_output_format.py, docs/guide/tasks.md and the refusal-hint strings. R: removes a user-facing command with no alias; LESSONS.md tells agents to run `task relations`. |
| E-727 | Configurable date/time output format | no | medium | medium | C: key name, syntax (strftime vs presets), per-user vs per-project, and surfaces covered are all open. R: adds a key to the user config.json format; a bad format garbles every timestamp. |
| E-728 | Apply config date/time format to task show | description | medium | low | Blocked on E-727's design; scope unclear (session_cmd.py duplicates the formatter). R: display only. |
| E-1146 | Comma-separated IDs on `task add --relates-to` | no | medium | low | C: only --relates-to, or all ten relation flags plus `epic add`? R: contained arg parsing (TaskIDType, cli.py). |
| E-1610 | Epic-child handoffs → parent analysis; epic field convention doc | yes (stale) | medium | low | Implements ED-1509. C: E-1531 is now assumed, so decide whether the guide documents the interim "sections in analysis" or typed content; template names changed. R: template + prose. |
| E-1545 | Document epic semantics in the guide | yes (stale) | medium | low | Guide still has a stub (help/epic.md, index.md). C: plan uses dead statuses and overlaps orchestration.md; behavior must be re-derived from epic_derivation.go. R: docs. |
| E-1769 | `session status --ascii` | yes (stale) | medium | low | C: E-2107/E-2128 added many glyphs since, and the plan's `~` collides with undeterminedGlyph; the mapping needs redesign and sign-off. R: opt-in, display only. |
| E-1537 | Epic type with auto-derived lifecycle | yes | medium | medium | Core is on main; open because children E-1543/1545/1546/1547 are ready and E-1584 is submitted. A container, not implementable; must never auto-spawn as an implementation task. |
| E-1543 | Promotion validation for `--type epic` | yes (stale) | medium | medium | Neither gate is on main (task_cmd.py). C: `superseded` and the abandonment-reason rules came later, so whether the sticky-only set is still right needs Mike. R: hard write-path gate on every epic status change. |
| E-1430 | Validate ENDLESS_SESSION_ID against sessions | yes | medium | high | Both defects are live (task_cmd.py; also monitor/verify_ownership.go). C: "is an ended session invalid?" is open; needs a new read-only Go lookup; the sandbox DB may lack the row. R: hot path — a wrong reject blocks every esu user's writes; a wrong accept misattributes actors in the ledger. |
| E-1703 | Refuse Write/Edit outside the bound worktree | yes | medium | high | E-1714 confirmed the gap (only the cwd is checked, hookcmd/claude.go). C: no answer yet for scratchpad, the user-level Claude dir, or tmp-symlink targets. R: an unconditional PreToolUse gate; a false positive blocks every agent on every project. |
| E-1712 | Consolidate PreToolUse edit gates (epic) | yes | medium | high | E-1711/E-1714 landed; E-1703 ready; E-1346 submitted and overlapping E-1586/E-1983. R: a wrong gate can brick the sessions needed to revert it. |
| E-1553 | Decide transitive block computation | yes | high | medium | No decision exists; parent transitivity is unimplemented (E-1795 did display-only chain transitivity). C: options A/B/C open and in tension with E-1708. R: sets blocking semantics for claim, next, status and guide. |
| E-800 | Backend integration: Beads/JIRA/GH/Notion (epic) | yes | high | high | C: 7 children; bidirectional sync "needs discussion"; depends on the open ID-scheme brainstorm E-1831. R: sync writes through the ledger, adds a config format (E-849) and a migration with rollback (E-821). |
| E-894 | Move task display reads from Python to Go | yes (stale) | high | high | Not on main; blocks E-1730/E-2035/E-2082. C: 3-phase umbrella; the plan cites the removed internal/web and endless-event; scope may need reconciling with E-1063. R: deletes db.py schema bootstrap and migrations; rewires every task read. |
| E-1829 | Distributed multi-developer collaboration (epic) | yes | high | high | Children E-800, E-1830 and E-1831, plus E-1935, are all open. R: task-ID format, ledger rebuild, cross-developer sync. |

## 3. Close candidates — done, obsolete, or superseded (not closed; Mike's call)

| ID | Title | Verdict | C | R | Evidence |
|---|---|---|---|---|---|
| E-1132 | `--analysis` on task update | already done | medium* | low | cli.py: --analysis, --analysis-file, --clear analysis. |
| E-1133 | `--notes` on task update | already done | medium* | low | cli.py: --notes, --notes-file, --clear notes. |
| E-1326 | `--analysis` on task add/update from file | already done | medium* | low | cli.py. |
| E-1479 | `worktree land --record-only` | core done | medium | high | E-1719 shipped `--record-only --sha [--at]` (cli.py). Leftovers (auto-discovery, (task,sha) dedup, --session-id) were never requested and wait on E-1486; they would write landings into the ledger. The plan is stale (E-2108 dropped the branch column). Close, or cut down to the extras. |
| E-1086 | Per-project worktree-creation hook | superseded by E-986 | medium* | medium | E-986 shipped .endless/hooks/post-worktree-create.sh (worktree_cmd.py). **E-1193 (submitted) proposes the reverse — closing E-986 as a dup of E-1086 — and is now backwards.** |
| E-1113 | Event-commit workflow for non-land merges | superseded by E-1206 | high | high | E-1206 commits every ledger segment at write time (internal/events/commit.go); `.endless/events/` no longer exists. |
| E-1654 | Guard `rebuild-db --confirm` vs lossy projection | superseded by E-2062 | medium* | high | E-2062 refuses `--confirm` unconditionally (eventcmd/event.go), which is stricter than asked. The long-term cure is E-1671. |
| E-1724 | Make MEMORY.md workable at scale (research) | superseded by E-2057 | medium | low | Auto memory is off (.claude/settings.json); lessons replace it (E-2007); E-2057 renders MEMORY.md as a budgeted index. |
| E-1098 | Broaden isPlanFile to PLAN_*.md | obsolete | medium* | low | Plan snapshots were deleted (E-1448/E-1449). isPlanFile (hookcmd/claude.go) now only gates a "Plan file synced" message, so broadening it spreads a misleading message. |
| E-1143 | Tier column/flag on task search | obsolete | medium | low | E-1813 dropped tasks.tier (migration 00008). The surviving gap is complexity/risk filters on `task search` (task list has them); retitle if wanted. |
| E-729 | Post-mortem section in task show | obsolete | medium | low | Outcome is the post-mortem by convention (E-787), and task show already renders "— Outcome —". Parent E-721 is unplanned and blocked. |
| E-1547 | Investigate active_task_id cleared on session 614 | obsolete | medium | low | Column renamed and made write-once (E-1969); suspected causes fixed (E-1530, E-1640, E-1700, E-1856). E-1339 may also be superseded by E-1969. |

\* Complexity raised above the intrinsic `low` so the row can't auto-spawn (see top).

## 4. Unsure — needs a keep / re-scope decision

| ID | Title | Plan | C | R | Why unsure |
|---|---|---|---|---|---|
| E-850 | Beads `bd --json` integration tests | no | medium | low | No Beads code on main yet (E-818/E-819 unplanned), so nothing says which fields to pin. How tests get `bd` is undecided. Premature. |
| E-1147 | `--relates-to` on `task link` | no | medium | low | `task link --to X --type relates_to` already works. The shortcut's shape (one flag vs one per type, whether --to/--type become optional) is unspecified. |
| E-1435 | Flat table for task links output | yes (stale) | medium | low | E-1477/E-1576 already made a flat, direction-explicit list shared with task show. A separate table would split that shared format again. |
| E-1546 | Guide: noun-aliased verbs as AI-preferred | yes (stale) | medium | low | Most targets are already gone; the remainder overlaps E-1545; `endless epic --help` calls itself the "human-facing alias", which contradicts the premise. |
| E-1451 | Non-Claude shell ledger writes, harness=shell | yes (stale) | high | high | E-1444 `--no-session` already unblocks shell writes. Attribution-only remainder; blockers E-1407/E-1450 open; harness=shell would defeat the empty-harness exemption (status_transition.go). |
| E-1721 | MEMORY.md lessons → features (epic) | no | high | medium | Memory is OFF here and lessons go through `endless lesson write`; E-2007/E-2057 overlap. The claiming session ES-867 is stuck in needs_input. |
| E-1741 | Dispositions for 17 MEMORY.md (a) candidates | yes | high | medium | A one-at-a-time interview with Mike. Its "prune MEMORY.md on land" step now violates project rules; candidates should be re-triaged against LESSONS.md. |

## Already rated (unchanged)

E-1814 (Compute auto-spawn eligibility) — medium/medium, rated before this task.
