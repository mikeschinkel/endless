## Research request

**Input:** docs/research-2026-10-14-claude-hooks-and-mods-across-platforms.md (sections 1, 4 and 5 especially: the execution/control matrix, failure modes and gates, and the ranked options).

**Scope:** Endless's own code, accepted decisions, and open and settled tasks — not further research into Claude Code itself, except where a claim in the report must be confirmed to answer a question below (use the report's section 7 probe list for that, and say which claims were taken on trust).

### Questions to answer

1. **Inventory of assumptions.** Where do Endless's code and decisions assume hooks fired? List each invariant the hooks enforce or record (task claim before Write/Edit, session registration, worktree routing, ledger auto-commits, session/harness identity, verify/land gates, status transitions, injected context) and what Endless does today when that hook never runs. Search the Go hook (`internal/hookcmd` and the `endless-go` hook entry points), the installed hook settings, and the Python CLI for code that reads session state the hooks write.
2. **Failure analysis per surface.** For each row of the report's matrix that can touch an Endless-managed repo (CCC local, CCC + Remote Control, `claude remote-control` server mode, CCD local/Dispatch/scheduled, cloud, routine, Open in > Cloud, teleported), what goes wrong for Endless: silent unmanaged edits, commits on a task branch with no session, ledger divergence, a task shown underway with no live session, pushes from a cloud VM landing on branches Endless owns, a stale or wrong database written, and so on. Rate each by likelihood and blast radius.
3. **Fail-open vs fail-closed today.** Which installed hooks fail open (missing binary, timeout, crash, non-blocking exit) and which block? Do any rely on `onFailure: "block"`? What Claude Code version floor does Endless currently require or check?
4. **Detection.** Can Endless detect after the fact that work happened without hooks — commits on a task branch or main with no matching session, ledger gaps, a SessionStart that never arrived for a session Endless launched? What signals exist (`CLAUDE_CODE_REMOTE`, `CLAUDE_CODE_BRIDGE_SESSION_ID`, a SessionStart nonce, git history) and where would a check belong?
5. **Existing coverage.** Which open tasks and decisions already address part of this (for example the tasks on failing loudly in hooks, making hook errors block, which hooks run outside an Endless tmux server, which hook events to install, Claude Desktop support, and per-project hook install), and which gaps are unowned?
6. **PRODUCT view.** How should this behave for another user on another machine managing a project that is not Endless — including collaborators who open the repo in a cloud session without Endless, so a committed guard must not break their unrelated work?

### Deliverable

An `outcome` that gives: the list of hardening gaps ranked by risk; for each, the recommended gate (block, detect-and-flag, document as unsupported) and whether an existing task owns it; proposed follow-up tasks (filed, and linked from this task with `cleans_up`/`relates_to`) for unowned gaps; and any decision Endless should record (for instance "Endless does not support cloud execution" or a minimum Claude Code version). If the findings are long, write them to `docs/research-<date>-endless-hook-hardening.md` and summarize in the outcome.
