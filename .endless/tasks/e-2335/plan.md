## Research request

**Input:** docs/research-2026-10-14-claude-hooks-and-mods-across-platforms.md (sections 1, 4 and 5 especially: the execution/control matrix, failure modes and gates, and the ranked options).

**Scope (Mike, 2026-10-14):**
- Supported surfaces: (1) CCC sessions in an Endless tmux server, (2) CCC + Remote Control steered from CW/CM/CCD, (3) CCD local sessions on the user's machine. Cloud execution (CW, CM, CCD Cloud, Open in > Cloud, `claude --cloud`, routines) is explicitly **blocked if possible, for now**. How Endless could support cloud later is a separate research or brainstorm task, filed as a follow-up.
- Claude Code only. Other harnesses and humans editing without hooks are out of scope (they belong with the hook adaptor-layer work).
- The report's recommendations are Claude Research's, not Mike's: evaluate them, don't adopt them.

**Probes:** verify the report's claims hands-on for every supported surface (CCC, CCC + Remote Control including server-mode slots, CCD local: new, resumed, worktree, Dispatch, scheduled), plus the failure and off-switch behaviors (`onFailure: "block"` with a missing binary, `disableAllHooks`, `--safe-mode`, `--bare`). No probes of cloud support itself, EXCEPT probes of how to keep cloud sessions from causing problems in repos managed by Endless (for example, whether a committed guard blocks a cloud session, and what a cloud session can push to branches Endless owns). Probes that need a Desktop, web or phone session are prepared as a kit with steps for Mike to run. Probes must not write to Endless's real database or ledger.

### Questions to answer

1. **Inventory of assumptions.** Where do Endless's code and decisions assume hooks fired? List each invariant the hooks enforce or record (task claim before Write/Edit, session registration, worktree routing, ledger auto-commits, session/harness identity, verify/land gates, status transitions, injected context) and what Endless does today when that hook never runs. Search the Go hook (`internal/hookcmd` and the `endless-go` hook entry points), the installed hook settings, and the Python CLI for code that reads session state the hooks write.
2. **Failure analysis per surface.** For each row of the report's matrix that can touch an Endless-managed repo (CCC local, CCC + Remote Control, `claude remote-control` server mode, CCD local/Dispatch/scheduled, cloud, routine, Open in > Cloud, teleported), what goes wrong for Endless: silent unmanaged edits, commits on a task branch with no session, ledger divergence, a task shown underway with no live session, pushes from a cloud VM landing on branches Endless owns, a stale or wrong database written, and so on. Rate each by likelihood and blast radius.
3. **Fail-open vs fail-closed today.** Which installed hooks fail open (missing binary, timeout, crash, non-blocking exit) and which block? Do any rely on `onFailure: "block"`? What Claude Code version floor does Endless currently require or check?
4. **Detection.** Can Endless detect after the fact that work happened without hooks — commits on a task branch or main with no matching session, ledger gaps, a SessionStart that never arrived for a session Endless launched? What signals exist (`CLAUDE_CODE_REMOTE`, `CLAUDE_CODE_BRIDGE_SESSION_ID`, a SessionStart nonce, git history) and where would a check belong?
5. **Existing coverage.** Which open tasks and decisions already address part of this (for example the tasks on failing loudly in hooks, making hook errors block, which hooks run outside an Endless tmux server, which hook events to install, Claude Desktop support, and per-project hook install), and which gaps are unowned?
6. **PRODUCT view.** How should this behave for another user on another machine managing a project that is not Endless — including collaborators who open the repo in a cloud session without Endless, so a committed guard must not break their unrelated work?

### Deliverable

The task's `outcome` (Mike's choice: outcome, not a separate doc) gives: probe results; the list of hardening gaps ranked by risk; for each, the proposed gate (block, detect-and-flag, document as unsupported) and whether an existing task owns it; and any decision Endless should record (for instance a minimum Claude Code version). Follow-up tasks for unowned gaps are filed and linked from this task with `cleans_up`, including one to research or brainstorm future cloud support. Mike expects follow-up questions and possibly more research after reading.
