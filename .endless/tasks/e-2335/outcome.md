# E-2335 findings (DRAFT: interim; probes on Desktop, Remote Control and cloud are still pending)

Scope, as Mike set it: Endless supports CCC in an Endless tmux server, CCC + Remote Control, and CCD local sessions. Cloud sessions are blocked for now if possible. Claude Code only. The Claude Research report is an input to evaluate; it is not the plan.

## 1. Local probe results (Claude Code v2.1.300, headless `claude -p`, project-scope hooks in a scratch repo)

| Probe | Result |
|---|---|
| PreToolUse hook whose binary is missing, no `onFailure` | Write **went through** silently (fails open) |
| Same, `onFailure: "block"` | Write **blocked**; the agent did not route around it via Bash |
| Hook that crashes (exit 1), no `onFailure` | Write went through |
| Same, `onFailure: "block"` | Blocked |
| Hook that times out (`timeout: 5`), `onFailure: "block"` | Blocked |
| UserPromptSubmit, binary missing, `onFailure: "block"` | **The whole prompt is refused before the model runs**, and the hook's stderr is shown |
| SessionStart, binary missing, `onFailure: "block"` | **No effect**: the session ran and wrote the file |
| `disableAllHooks: true` in project settings | All hooks off, including one with `onFailure: "block"` |
| `--settings '{"disableAllHooks":true}'` | All hooks off |
| `--safe-mode` | All hooks off |
| `--bare` | Could not test on an OAuth login ("Not logged in"); documented to skip hooks |
| A committed shim guard that exits 2 when `CLAUDE_CODE_REMOTE=true` (UserPromptSubmit, `onFailure: block`), with cloud simulated by env | Prompt refused with the Endless message; passes locally |

Surface signals seen: an interactive CLI session has `CLAUDE_CODE_ENTRYPOINT=cli`; **headless `claude -p` has `sdk-cli`** and `CLAUDE_CODE_SESSION_ATTENDED=0`.

## 2. What Endless depends on hooks for (code review)

- **Every worktree discipline gate is PreToolUse-only:** no edits or commits on main, cwd inside the claimed worktree, one session per worktree, wrong-worktree writes, worktree removal being user-only (ED-1578), sqlite against `.endless`, Bash writes, and the revisit gate. With no hook running, none of these exist.
- **Session-to-task binding happens only at SessionStart** (spawn-bind and cwd-bind, since E-1983). `task claim` self-heals a session row through `CLAUDECODE` + `CLAUDE_CODE_SESSION_ID`, but a spawned task's session is never bound.
- **Session state is never maintained without hooks:** working, idle, needs_input, ended, pane mapping, worktree locks, delivery of change notices, transcript parsing.
- **Hook installation is user scope only** (`endless setup claude-hook` writes the user settings file). The committed `.claude/settings.json` has no hooks, and worktrees get none (ED-1596). No entry uses `onFailure`, no entry sets `timeout`, and there is **no minimum Claude Code version check**.
- **How the hook fails today:** handler errors on Pre/PostToolUse, UserPromptSubmit and SessionStart exit 2 (ED-1357, E-1661). Deliberate exit-0 paths: unsupported harness, `ENDLESS_NO_HOOKS`, empty cwd, an ignored dir, schema refusal, an unregistered project (gates skipped). A missing binary, crash before main, or timeout is decided by Claude Code: **it fails open**, as confirmed by the probe.
- **Surface detection is `CLAUDE_CODE_ENTRYPOINT` only** (agentenv). `cli` is supported; `claude-desktop` is **unsupported, so the hook no-ops and the CLI prints an "unsupported" banner**. Anything else is "Unknown", which the CLI treats as **a human**. `CLAUDE_CODE_REMOTE`, `CLAUDE_CODE_BRIDGE_SESSION_ID` and `CLAUDE_CODE_CHILD_SESSION` are never read.

## 3. Gaps, ranked by risk (draft)

1. **Unreviewed inbound changes to tracked executables and config.** `.endless/hooks/*.sh` (post-land, post-worktree-create, seed-sandbox, pre-land), `tasks/e-*/verify.sh`, `_harness.sh`, `config.json` and `verbs.jsonl` are tracked. `mainsyncjob` fast-forwards main from origin with no inspection when only origin moved. A cloud session that pushes to main (its own Stop hook prompts commit and push) gets scripts **executed on the Mac** at the next land, worktree create or verify. Unowned.
2. **Hooks fail open when the binary is missing, crashes or times out.** Fix: `onFailure: "block"` on PreToolUse and UserPromptSubmit, plus a version floor of v2.1.295 or later. The UserPromptSubmit block is the stronger gate, because it refuses before the model acts. Unowned: E-1358 and ED-1357 cover only errors raised by a running binary.
3. **Cloud sessions run unmanaged.** They don't load user scope, which is the only place Endless installs. Blocking them requires something committed to the repo, so the cloud gate and E-2134 (per-project hooks) are the same design question. **PRODUCT:** a committed guard affects every collaborator on the repo. The shim form (exit 2 only when `CLAUDE_CODE_REMOTE=true`) leaves local non-Endless collaborators alone but blocks their cloud sessions too. Unowned.
4. **Claude Desktop is declared unsupported in code (agentenv), contradicting the new scope.** Supporting it needs: the hook to run for `claude-desktop`, session identity without tmux (E-1505), verification that hooks actually fire (pending probe), and PATH and binary resolution when launched from the Dock. Partly owned by E-1505 (identity only).
5. **Silent hookless runs leave state that looks plausible.** A spawned task sits `underway` with no claimant, and a second spawn is allowed. **The auto-spawn cap counts only sessions joined to tasks, so hookless auto-spawns are uncapped.** Nothing waits for or checks the SessionStart that the spawn expects. Unowned (E-2300 covers tmux liveness, not "hooks never fired").
6. **Headless and unknown entrypoints are treated as human.** `sdk-cli` (any `claude -p`, including SDK harnesses) → Unknown → agent-only CLI refusals (E-2162, E-1403) don't apply. Partly owned by E-2162 and E-1403, whose detection is entrypoint-based.
7. **Off-switches an agent can flip:** `ENDLESS_NO_HOOKS` (ED-1471) is honoured from any environment, and `disableAllHooks` or `--safe-mode` disable everything, `onFailure` included. Detecting these is only possible after the fact (git or ledger) or through a doctor check (E-2285). Unowned.
8. **Ledger lines arriving by git are never validated.** Only the manual `validate-db` compares ledger against DB, and it raises no fault. Edited mirrors are silently overwritten by docsweep. Unowned; relates to ED-1551.
9. **Nothing detects commits on main that are neither Endless auto-commits nor landings, or remote branches outside `task/*`.** These are cheap to detect: `AutoCommitSubjectPrefix` and `task_landings` against `git log`, and `git ls-remote`. Unowned.
10. **Remote Control:** no task mentions it. Execution stays local, so hooks run (to be confirmed by probe 1a), but server-mode slots were reported not firing hooks (probe 1b).

## 4. Task text that contradicts accepted fail-closed policy (ED-1357)

E-2063 ("hooks fail open by design"), E-1972 ("hook paths already fail open") and E-1887's plan ("must not block on a database failure"). ED-1519 (block on integrity failure) is still `proposed` although E-1661 shipped it.

## 5. Evaluating the report's recommendations (draft)

- *Option 1, local-only + `onFailure: "block"`:* the gate is confirmed to work locally and is cheap. It does not address Desktop, which Mike wants supported rather than refused.
- *The SessionStart heartbeat or nonce:* a check that Endless's own spawn path received a SessionStart can be made in Endless itself (spawn writes an expectation; a job flags it if no session binds within N minutes). This also closes the auto-spawn cap leak. Worth doing.
- *Option 2, a committed cloud guard:* the shim works in a simulated cloud. It must be verified on a real cloud VM (probe 3), and it is a per-project install decision (E-2134).
- *Option 4, cloud via a synced plugin:* out of scope now. A follow-up research task covers future cloud support.
