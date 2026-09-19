`endless setup claude-hook` writes Endless's seven hook entries into the USER's
Claude Code settings, machine-wide. So every Claude Code session on the machine,
in any directory, invokes `endless-go hook claude` on every hooked event —
including in projects that have nothing to do with Endless, and will never have
anything to do with it.

Three reasons that is the wrong level, and the third is the load-bearing one:

- PERFORMANCE. A process spawn per hooked event, per session, per project on the
  machine, to do nothing.
- COMPLEXITY. It forces a runtime bypass to exist and stay correct —
  `agentenv.Supported`, `shouldSkipForWorktree`, the unregistered-project path.
  That machinery exists only because the hook is reachable where it should not
  be.
- ROBUSTNESS. A bug in the bypass means Endless acts on a project that never
  opted in. A hook that was never registered cannot have that bug. Removing the
  reachability is strictly stronger than gating it, and "the gate works today"
  is not an argument against removing it.

It also makes Endless opt-OUT rather than opt-in. `monitor.ProjectIDForPath`
auto-registers any directory a session runs in as an active project — 52 rows
on the development machine as of 2026-09-11, including the home directory. That is downstream of the
hook firing, so scoping the hook subsumes it rather than competing with it.

WHAT TO BUILD

`project init` writes the hook entries into the project's own
`.claude/settings.json`. The hook command is a committed, project-relative loader
script rather than an absolute path, so the committed file carries nothing
machine-specific: the loader locates `endless-go`, and when it cannot, tells
Claude to ask the user to approve installing it. The bootstrap cost is therefore
first-run-only, not permanent — which is what makes `git clone` and go real
rather than aspirational.

MIGRATION

The user-level entries must be removed, not merely supplemented: Claude Code
merges hooks across settings levels, so a tracked project would otherwise
double-fire every event.

CONSTRAINT — DO NOT FORECLOSE A USER-MACHINE LAYER

Endless is expected to split its database into a user-machine half (projects,
sessions) and a project half (tasks). When that lands, some hook
responsibilities may legitimately belong at the user-machine level — "which
session is this, which pane, is it alive" is user-machine; "does this session
hold a task, may it write" is project. The end state is plausibly BOTH levels.

This task scopes installation to the project and leaves the per-responsibility
partition to follow the DB work. It deliberately does NOT block on that split —
this is the easier of the two, and shipping it first does not constrain it.
