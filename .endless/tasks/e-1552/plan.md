# E-1552 — Decide endless task spawn behavior when invoked on various task types

## 1. Scope

Decide how `endless task spawn` behaves across the four task types
(`task`, `bug`, `research`, `epic`), each combined with the state of the
task's children (zero / all `needs_plan` / all `ready` / all `in_progress`
/ all terminal / mixed). Today's spawn is type-blind and assumes a leaf
implementation task; that breaks for `epic` (a container per E-1537) and
is imprecise for `bug` and `research` whose deliverable shapes differ.

The impetus is the introduction of `epic` via E-1537/E-1538, but the
research delivers a single coherent design covering all four typed
shapes, not just epic. Tmux-window proliferation under multi-task
coordination is a load-bearing secondary motivation.

## 2. Cases to design

Cross-product of {type} × {children-state}:

- **Types**: `task`, `bug`, `research`, `epic`
- **Children states**: zero / all `needs_plan` / all `ready` / all
  `in_progress` / all terminal / mixed

24 cells. Design must be coherent across them.

## 3. Open design questions

- **Resolution mechanism**: auto-detect from children state, `--mode`
  flag, interactive choice, or combination?
- **Claim binding**: spawn pre-claim the parent (today's flow)? For
  epic, does the coordinator claim children, or do dispatched sessions
  claim themselves?
- **Worktree creation timing**: per-child worktrees at coordinator
  dispatch vs. dispatched-session self-claim?
- **Background-session integration** (Agent View / `claude --bg`): can
  spawn dispatch via bg agents to avoid tmux-window proliferation?
  What carrier mechanism binds bg agents to endless tasks?
- **Worktree conflict with Claude's `isolation:"worktree"`**: how does
  endless coexist with Claude's nested worktree behavior under bg?
- **DB schema for bg agents**: how does `sessions` model background
  agents (no tmux_pane) and the coordinator→bg-agent view-switching?
- **Attach mechanisms**: opening an existing bg agent in a new tmux
  window vs. the current pane?
- **Template format and location**: stay with Python `string.Template`,
  move to Go `text/template`, or jinja2? Where do templates live?
- **Throttling**: should endless enforce / warn / ignore parallel bg
  agent limits (quota burns at N×, community sweet spot ~3-5)?
- **Session lifecycle**: recovery after machine sleep / shutdown / tmux
  crash — what does endless need to surface?
- **Explore sub-agent dispatch** for the planning case: does endless
  own template prompts, or does the coordinator construct them?
- **Relationship to E-1136**: interactive coordinator (this work) vs.
  unattended daemon (E-1136) — co-exist or one obsoletes the other?
- **One-session-per-task invariant**: today's assumption. Does bg-agent
  dispatch + coordinator viewing require bending it?

## 4. Out of scope

- Implementation of the design (filed as a follow-up epic).
- The `WorktreeCreate` hook integration as a long-term replacement for
  `bgIsolation:"none"` (filed as maybe-task E-1559).
- Non-Claude agent harness carrier mechanism (filed as maybe-task
  E-1558).
- Session restoration commands (filed as next-phase E-1560, E-1561).

## 5. Inputs

- `docs/research-2026-06-10-claude-multi-agent-usage.md` — multi-agent
  orchestration research surfacing Model 1 (planning orchestrator) and
  Model 2 (implementation dispatcher).
- `docs/research-2026-06-12-claude-background-agents.md` (delivered by
  E-1557) — Claude Code Agent View mechanics: `claude --bg`,
  `claude attach`, `worktree.bgIsolation`, session lifecycle.
- E-1537 §6.5 — coordinator session semantics this work extends.
- E-1538 — `task_types` FK seeding (gates implementation, in flight).

## 6. Deliverable

Decisions on each of the §3 open questions, captured in
`tasks.outcome`. Implementation work filed as a follow-up epic with
per-implementation child tasks. Decision record via
`endless decision add` for any design choices that transcend a single
implementation task.
