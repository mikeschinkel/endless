# E-2335 findings: hardening Endless against Claude sessions its hooks don't govern

Scope (Mike, 2026-10-14):
- Supported: CCC in an Endless tmux server; CCC + Remote Control.
- Claude Desktop: deliberately unsupported (the agentenv skip is intentional); the goal is **detect and warn**.
- Cloud execution: **blocked if possible, for now**; future support is a separate research task.
- Claude Code only. The Claude Research report is input to evaluate, not the plan.

## 1. Probe results (Claude Code v2.1.300; Desktop's bundled SDK 2.1.295)

### Failure handling and off-switches
Headless sessions, with a project-scope hook in a scratch repo:

| Probe | Result |
|---|---|
| PreToolUse binary missing, no `onFailure` | Write **went through**: fails open |
| Same, with `onFailure: "block"` | **Blocked**; the agent did not route around it via Bash |
| Hook crashes (exit 1), without / with `onFailure: "block"` | Went through / **blocked** |
| Hook times out, with `onFailure: "block"` | **Blocked** |
| UserPromptSubmit missing, with `onFailure: "block"` | **Whole prompt refused before the model runs**; stderr shown |
| SessionStart missing, with `onFailure: "block"` | **No effect**: the session ran |
| `disableAllHooks` (project settings or `--settings`), `--safe-mode` | **Every hook off**, including `onFailure: "block"` |
| `--bare` | Untestable on an OAuth login; documented to skip hooks |

### Surfaces

- **CCC + Remote Control (`claude --remote-control`), prompts sent from the iPad:**
  - User- and project-scope hooks **all fired**: UserPromptSubmit, Pre/PostToolUse, Stop.
  - Entrypoint is `cli`.
  - Every event after Remote Control connected carries `CLAUDE_CODE_BRIDGE_SESSION_ID` (absent at SessionStart).
  - **Covered by Endless's existing hook.**
- **Remote Control server mode (`claude remote-control`), two sessions spawned from the iPad:**
  - Hooks **fired** at both scopes, for every event, in both slots. The report's "extra slots don't fire hooks" did not reproduce. One user-scope SessionStart line was missing.
  - BUT the entrypoint is **`sdk-cli`**, with no bridge marker. agentenv classifies that as Unknown, and `supportedAgent()` is false for Unknown, so **Endless's hook fires and silently exits 0**. A session started from the phone's device card in an Endless repo is **completely unmanaged**.
  - The same applies to any `claude -p` session (`sdk-cli`), which the CLI also treats as **a human**.
- **Cloud, guard variant A** (committed shim that exits 2 when `CLAUDE_CODE_REMOTE=true`, on SessionStart/UserPromptSubmit/PreToolUse, `onFailure: block`), real session via `claude --cloud`:
  - **Every prompt refused** before the model ran, with the Endless message and the `cse_…` id.
  - Nothing pushed.
- **Cloud, guard variant B** (committed hook pointing at a binary that exists only on the Mac, `onFailure: block`):
  - **Every prompt refused.** Endless's ordinary hook entry, committed with `onFailure: block`, is by itself a cloud block. A shim adds only a clear message and a pass-through for collaborators.
- **Cloud, unguarded: what actually happened from the iPad.**
  - A cloud session on `mikeschinkel/endless` created a file.
  - Anthropic's own cloud Stop hook ("Hook re-prompted Claude") then made it **commit and push branch `claude/add-rc1-txt` to the real Endless repo without being asked**. Mike deleted it; the remote is clean (main only).
  - That session's git proxy refused branch **deletes** (403) but allowed **pushes**.
- **Cloud: guard bypass.**
  - The same session (started on `endless`) then **cloned `endless-cloud-probe` mid-session and pushed to it**.
  - A cloud session loads hooks only from the repo it started on, so **a repo cloned mid-session is never guarded**, whatever it commits.
- **Claude Desktop (current build, no Code tab; Desktop picks the route).** A prompt on a local folder:
  - Ran as a **remote session** (`cse_` id), with **tools run by `remote-bash` in the local Cowork VM**.
  - The folder was mounted **read-write** after a consent dialog.
  - No Claude Code process ran on the Mac and no transcript was written, so **no hook fired at any scope**, yet the file landed in the checkout. Endless's own Desktop skip never comes into play.
  - The only conceivable in-session hook is one from a plugin enabled on the claude.ai account. It would run off the Mac and so can't reach Endless. Cowork plugin hooks also have a history of not firing (#27398 → #16288). Not probed: Mike has not yet decided whether to allow an account-plugin probe.
- Not probed:
  - multi-repo cloud sessions
  - Desktop **Open in > Cloud**
  - managed-settings hooks under `--safe-mode`

## 2. What Endless depends on hooks for (code review)

- **Every worktree-discipline gate is PreToolUse-only:**
  - no edits or commits on main
  - cwd inside the claimed worktree
  - one session per worktree
  - wrong-worktree writes
  - user-only worktree removal (ED-1578)
  - sqlite against `.endless`
  - Bash write targets
  - the revisit gate
- **Session-to-task binding happens only at SessionStart.** `task claim` self-heals a session row via `CLAUDECODE` + `CLAUDE_CODE_SESSION_ID`, but a spawned task's session is never bound.
- **Session state is lost without hooks:** working/idle/needs_input/ended, pane mapping, worktree locks, change-notice delivery, transcript parsing.
- **Installation** (`endless setup claude-hook`):
  - user scope only
  - no `onFailure`, no `timeout`
  - **no minimum Claude Code version check**
  - the committed `.claude/settings.json` and worktrees have no hooks (ED-1596)
- **Hook failure today:**
  - Handler errors on Pre/PostToolUse, UserPromptSubmit and SessionStart exit 2 (ED-1357, E-1661).
  - Deliberate exit-0 paths: unsupported or Unknown harness, `ENDLESS_NO_HOOKS`, empty cwd, ignored dir, schema refusal, unregistered project.
  - A missing binary or timeout **fails open**: the probe confirms it.
- **Detection** uses `CLAUDE_CODE_ENTRYPOINT` only. `CLAUDE_CODE_REMOTE`, `CLAUDE_CODE_BRIDGE_SESSION_ID` and `CLAUDE_CODE_CHILD_SESSION` are never read.

## 3. Gaps, ranked by risk

1. **Inbound commits are executed unreviewed.**
   - `.endless/hooks/*.sh` (post-land, post-worktree-create, seed-sandbox, pre-land), `tasks/e-*/verify.sh`, `_harness.sh`, `config.json` and `verbs.jsonl` are tracked.
   - `mainsyncjob` fast-forwards main from origin with no inspection.
   - A cloud session that pushes to main gets scripts **executed on the Mac** at the next land, worktree create or verify. The iPad incident proved cloud sessions push unprompted.
   - Owner: **E-2338**.
2. **Cloud sessions on Endless repos run unmanaged, and push.**
   - A committed guard blocks sessions *started on* the repo; both variants are proven.
   - But a session started on any other repo can clone and push, so the guard is necessary but not sufficient. Gap 1's inbound inspection is the backstop.
   - **PRODUCT:** a committed guard affects every collaborator. The shim form lets local non-Endless collaborators through but blocks their cloud sessions too.
   - Owner: **E-2339**, related to E-2134 (per-project hooks).
3. **Hooks fail open on a missing binary, crash or timeout.**
   - Fix: `onFailure: "block"` on PreToolUse and UserPromptSubmit (UserPromptSubmit is the stronger gate), plus a Claude Code version floor of 2.1.295 or later checked by setup.
   - E-1358 and ED-1357 cover only errors from a running binary.
   - Owner: **E-2340**.
4. **`sdk-cli` sessions are silently unmanaged.** This covers Remote Control server-mode spawns, which are part of the supported Remote Control surface, and any `claude -p` on an Endless repo. The hook no-ops and the CLI treats the agent as a human. This needs a decision, not just code.
   - Owner: **E-2341** (brainstorm). E-2162 and E-1403 depend on its answer.
5. **Work nobody accounts for goes unnoticed:**
   - Desktop/Cowork edits in a checkout or worktree
   - commits on main that are neither Endless auto-commits nor landings
   - remote branches outside `task/*` (e.g. `claude/*`)

   Detect and warn; no hook can block Desktop.
   - Owner: **E-2342**.
6. **A spawn whose SessionStart never arrives is invisible.** The task sits `underway` with no claimant and a second spawn is allowed. **The auto-spawn cap counts only sessions joined to tasks, so hookless auto-spawns are uncapped.**
   - Owner: **E-2343** (bugfix).
7. **Off-switches an agent can flip:** `ENDLESS_NO_HOOKS` is honoured from any environment; `disableAllHooks` and `--safe-mode` override even `onFailure: block`. These can only be detected after the fact (gap 5) or by a doctor check.
   - Suggest adding them to the check registry in E-2285. Not filed separately.
8. **Ledger lines arriving by git are never validated.** Only the manual `validate-db` compares them, and it raises no fault.
   - Partly covered by gap 1's inspection; relates to ED-1551.

## 4. Task and decision text that contradicts accepted fail-closed policy (ED-1357)

- E-2063 ("hooks fail open by design")
- E-1972 ("hook paths already fail open")
- E-1887's plan ("must not block on a database failure")
- ED-1519 (block on integrity failure) is still `proposed`, although E-1661 shipped it.

These are flagged for Mike, not edited.

## 5. The report's recommendations, evaluated

- **Option 1 (local only + `onFailure: block`):** confirmed to work and cheap. Adopt as gap 3. It does not help server mode, where hooks fire but Endless no-ops (gap 4), or Desktop, where no hook exists (gap 5).
- **SessionStart nonce/heartbeat:** worth doing in Endless's own spawn path (gap 6).
- **Option 2 (committed cloud guard):** proven on a real cloud VM, but bypassed by a clone mid-session. Adopt with gap 1 as the backstop.
- **Option 3 (Desktop "supported but verified"):** moot. Desktop no longer runs Claude Code locally, and Mike keeps it unsupported.
- **Option 4 (cloud via synced plugin):** deferred to the future-cloud research task, **E-2344**.
- **Report claims refuted or refined:** server-mode slots *do* fire hooks; Remote Control's bridge id appears only after connect.

## 6. Open questions for Mike

- Allow an account-plugin probe (a hook that refuses when `.endless/` is present) to see whether anything can stop Desktop/Cowork or cloud sessions in an Endless checkout?
- Delete the throwaway repo `mikeschinkel/endless-cloud-probe`. This needs the `delete_repo` scope on your login.
