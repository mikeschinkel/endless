# Why verify passes for agents but fails for Mike

## Answer

The largest cause, 13 of 31 cases and the only one specific to agents: **the verify runner passes the agent's own environment through to the suite.** `isolatedEnv` (internal/verifycmd/env.go) starts from `os.Environ()` and replaces only `HOME` and `XDG_CONFIG_HOME`. Everything else reaches the suite, including every variable Endless uses to tell an agent from a person. On top of that, the Python front door exports `ENDLESS_AUDIENCE=agent` before it starts the runner (cli.py, `agent_facing()` → `os.environ`). The suite therefore runs *as the agent*: the hook gate is open, refusals and footers render in agent form, and session resolution goes through `CLAUDE_CODE_SESSION_ID`. When Mike runs the same suite, none of that happens. The runner never fixed this at the source. Each of the 11 env-var cases was patched separately in its own suite, `conftest.py` or test, and the same lesson ("strip the agent env") was written down again each time.

Endless *intends* to behave differently for agents. The bug is that the runner lets the caller's identity decide what the suite tests.

The causes in the other cases are mostly not specific to agents (see the table). Usually the tree or the binary changed between the agent's run and Mike's, or the machine was under load.

## Reproduced today (minimal probe, not an old suite)

I committed a probe `verify.sh` that fails on purpose, so nothing could be recorded onto main. I ran it through `endless task verify E-2266` in three contexts, then removed it (commits 06d97b6d3 and 8084add03 on task/2266). The two user contexts were simulated by unsetting every `CLAUDE*`, `AI_AGENT` and `ENDLESS_AUDIENCE` variable, with and without `TMUX_PANE`. The tmux sibling-pane method from the E-2225 probe was refused by this session's auto-mode permission classifier.

| What the suite saw | Agent | User (in this tmux window) | User (no tmux) |
|---|---|---|---|
| `CLAUDECODE`, `CLAUDE_CODE_ENTRYPOINT=cli`, `CLAUDE_CODE_SESSION_ID`, `AI_AGENT`, `CLAUDE_CODE_MESSAGING_*` | present | absent | absent |
| `ENDLESS_AUDIENCE=agent` (exported by the front door) | present | absent | absent |
| `endless session id --db sandbox` | `1` (seeded from the agent's UUID) | `2` (a new row via the tmux window UUID) | **refuses**, exit 1 |
| `task show E-99999999` refusal | `Error: [Endless] … Handle this yourself: …` | `Error: No task found with id 99999999` | same as previous column |
| `task add` verb refusal | agent form with a "Decide:" block | two-line human form | same as previous column |
| provenance footer | `# db: sandbox (e-2266)` | `db: sandbox (e-2266)` | same as previous column |
| `endless` / `endless-go` on PATH | `~/.local/bin/endless`, `/usr/local/bin/endless-go` | same | same |

The hook gate (`agentenv.Supported()` → no-op without `CLAUDE_CODE_ENTRYPOINT=cli`) was not re-run here. Six sessions reproduced it independently (E-1917, E-1975, E-1814, E-940, E-2020, E-2251), and the code is unchanged (`supportedAgent()` in internal/hookcmd/agent_gate.go, called at the top of the Claude hook handler).

A trap in the reproduction method itself: `env $STRIP cmd` does **not** unset anything in zsh, because zsh does not word-split `$STRIP`. The E-1668 session fell into this trap and so did this one. Use bash or an array.

## Cases

From 529 transcripts since the runner first existed (2026-07-10), a regex prefilter found 244 candidates. Four parallel readers then read each candidate in context. Most hits were the spawn-prompt boilerplate. "Demo" means the session reproduced the cause; "guess" means it did not.

| # | Task | Date | What failed for Mike | Cause | Demo? | Intended or bug | Still possible today? |
|---|---|---|---|---|---|---|---|
| 1 | E-1782 | 07-14 | `--help` steering line missing | agent-only output form (agent detection) | guess | runner env leak (bug) | yes |
| 2 | E-1853 | 08-06 | 2 checks on "STOP / DO NOT reword" wording | `CLAUDECODE=1` selects the agent message | demo | runner env leak | yes |
| 3 | E-1898 (via E-1966) | 08-13 | `test_verb_gate_agent_form…` in `just test` | `CLAUDE_CODE_ENTRYPOINT` leaked into pytest; `conftest` stripped only `CLAUDECODE` | demo | env leak (fixed in conftest) | the conftest gap is closed; other tools are still exposed |
| 4 | E-1917 | 08-20 | 10/21: every hook-driven check | hook gate on `CLAUDE_CODE_ENTRYPOINT` | demo | runner env leak | yes |
| 5 | E-1975 | 08-21 | 16/106 hook checks | hook gate | demo | runner env leak | yes |
| 6 | E-2073 (2nd) | 08-26 | 8 live-hook refusal checks | session blamed `ENDLESS_NO_HOOKS` in Mike's shell. That was never checked, and Mike's ~/.zshrc, .zshenv and .zprofile don't set it. Most likely the hook gate (E-1962 had landed 13 days earlier). | guess | runner env leak (probable) | yes |
| 7 | E-2071 | 08-26 | `esu` refused: two sibling sessions on %413 | agent swept other tasks' suites directly. With `CLAUDE_CODE_ENTRYPOINT` set, e-1202's hook wrote a fixture session into **main** | demo | env leak + direct runs; since guarded by E-2023/E-2090 | direct runs are refused now; the leak remains |
| 8 | E-1668 | 09-17 | footer counted 0 | `# db:` (agent) vs `db:` (person) | demo | runner env leak | yes (probe row above) |
| 9 | E-940 | 09-30 | "temp database was not created" | hook gate | demo | runner env leak | yes |
| 10 | E-2020 (1st) | 09-30 | 4/64 incident checks | hook gate | demo | runner env leak | yes |
| 11 | E-1814 | 09-30 | 7/43 section C/D | hook gate | demo | runner env leak | yes |
| 12 | E-2251 (1st) | 10-06 | 2/16 auto-register checks | hook gate | demo | runner env leak | yes |
| 13 | E-1997 | 08-20 | helper banner empty | **user-only** `ENDLESS_SESSION_ID` from `esu` changes the helper's branch | demo | runner env leak (reverse direction) | yes |
| 14 | E-1898 | 08-10 | "pre-change DB has old triggers" | fixture built from `git show HEAD:`; agent ran before committing | demo | tree changed between runs | mostly closed by E-2243 (clean-tree refusal) |
| 15 | E-2073 (1st) | 08-26 | sweep found the suite itself | `git grep` saw the suite once it was committed | demo | tree changed between runs | mostly closed by E-2243 |
| 16 | E-2095 | 09-10 | "no file writes 'main'" | `git grep` skipped files the agent hadn't committed yet (vacuous pass) | demo | tree changed / vacuous check | mostly closed by E-2243 |
| 17 | E-2144 | 09-15 | stale-phrase sweep | agent wrote a plan file after its green run | demo | tree changed between runs | mostly closed by E-2243 |
| 18 | E-1906 | 08-07 | `needs_recap` sweep | main advanced and brought in another suite's DDL | demo | tree changed (rebase) | yes; suite-authoring issue |
| 19 | E-2107 | 09-06 | blast-radius diff, `just test` | Mike ran after land; merge-base was the task's own commit; stale worktree | demo | intended: a suite is a pre-land proof | n/a (intended) |
| 20 | E-1859 | 08-13 | 23/63 | worktree `bin/endless-go` replaced by a copy of main's binary | demo | stale binary (bug, cause unknown) | not established |
| 21 | E-1914 | 08-15 | 49/77 "no such column" | new binary vs stale worktree `schema.sql` (510 commits behind) | demo | stale tree vs binary | yes, when a worktree drifts |
| 22 | E-2030 | 09-06 | "no verification suite found" | worktree 585 behind; stale worktree binary | demo | stale tree/binary | yes, when a worktree drifts |
| 23 | E-2090 | 08-31 | test saw `ENDLESS_VERIFY_RUN` | land ran the **installed**, pre-change runner | demo | stale installed binary | yes in principle |
| 24 | E-1889 | 08-09 | `TestSupervisorSetsProcessGroup` 2s deadline | CPU load | guess | timing (tests since widened) | yes |
| 25 | E-1904 | 08-07 | `go test` hit the 10m package timeout | cold GOCACHE from `HOME` being replaced (E-1908) plus load | measured | isolation side effect (bug) | **yes, see below** |
| 26 | E-2186 | 10-02 | `go test` routing checks | same as #25 (runner temp HOME → cold GOCACHE) | guess | isolation side effect (bug) | **yes** |
| 27 | E-2137 | 09-16 | 7 hook-refusal checks | suite's own nested temp HOME → cold uv; registration failed silently | guess | suite authoring | n/a |
| 28 | E-2020 (2nd) | 09-30 | 2/66 | Mike's run overlapped the agent's edits | guess | concurrency | yes |
| 29 | E-2232 | 10-05 | `verify.sh: line 99: syntax error` | agent rewrote `verify.sh` mid-run; bash reads scripts incrementally | partly | concurrency | **yes** |
| 30 | E-2251 (2nd) | 10-06 | 1/2 pytest | unknown; the failed report was deleted when a later run passed | guess | concurrency (unconfirmed) | yes |
| 31 | E-1975 (2nd) | 08-21 | 2 minimizer invariants | nondeterministic LLM output exposed real bugs (fixed) | demo | real product bug | fixed |

Borderline, not counted above:
- E-1966: a second agent, not Mike, saw case 3's failure.
- E-2197: a pytest red/green split between two agents, from a guard keyed on `actor.Harness`.

Older causes that can no longer happen:
- `/var` vs `/private/var` temp HOME, fixed in af286afdc (E-2023).
- Suites run directly outside the runner, refused since E-2023/E-2090.
- CTRF overwritten by the next run, fixed in E-2243.

## By cause

| Cause | Cases | Specific to agent vs user? | Status |
|---|---|---|---|
| Runner passes the caller's harness/session env to the suite | 13 (1–13) | **yes**, the main cause | live, unfixed at the runner |
| Tree changed between the two runs (uncommitted, later edits, rebase, post-land) | 6 (14–19) | no, timing | mostly closed by E-2243's clean-tree rule; post-land is intended |
| Stale or mismatched binary vs tree | 4 (20–23) | no | live whenever a worktree drifts |
| Load / timing, amplified by a cold Go cache under the temp HOME | 3 (24–26) | no, load at run time | live |
| User run overlapped the agent's edits or runs | 3 (28–30) | no | live |
| Suite authoring (nested temp HOME, vacuous greps) | 2 (16, 27) | no | per suite |
| Nondeterministic output exposing a real bug | 1 (31) | no | fixed |

## Code review: everywhere Endless acts differently for an agent

What a suite inherits today, and what each variable switches:

- `CLAUDE_CODE_ENTRYPOINT` → `agentenv.Supported()` opens the hook gate (`supportedAgent()` callers in internal/hookcmd/claude.go: the hook entry and `reportChannelOn`). Without it, the hook is a silent no-op. Cause of 9 cases.
- `agentenv.Present()` (entrypoint or `__CFBundleIdentifier`) → `refusal.Agent()` (Go refusal rendering), `events.DetectedHarness()` (stamps `actor.harness` on every event a suite emits), and Python `agent_help.agent_facing()` (refusal form, `--help` directives, the `# db:` footer, the authority line, the claim handoff in task_cmd.py, `if not agent_env.present()`).
- `ENDLESS_AUDIENCE=agent`: set by the Python front door itself (cli.py, the root group, `os.environ[agent_help.AUDIENCE_VAR] = ...`) when an agent runs `endless task verify`, and inherited by every `endless` call inside the suite. Even a suite that strips `CLAUDE*` still gets agent rendering through this variable.
- `CLAUDECODE` + `CLAUDE_CODE_SESSION_ID` → layer 2 of `_current_endless_session_id` (`_current_endless_session_id` in task_cmd.py). Also `seedFromWorktree` (internal/sandboxcmd/seed_worktree.go) seeds the sandbox session row with the agent's real UUID. A person gets the null UUID. The sandbox reset runs *before* isolation under the caller's env (verifycmd/sandbox.go), so the seeded sandbox already differs by caller.
- `ENDLESS_SESSION_ID` (from Mike's `esu`) → layer 1 of session resolution, `callerTasks` in monitor/verify_scope.go, and the shell helpers. Cause of case 13.
- `TMUX_PANE` / the tmux window's `@endless_session_uuid` → layers 3–4. The probe got three different answers from three contexts.

## Also live, not specific to agents

- **Cold Go build cache.** Neither the agent's nor Mike's environment exports `GOCACHE`. The runner replaces `HOME`, so every `go test` in a suite builds from an empty cache under `$HOME/Library/Caches/go-build`. The same happens to `uv`'s cache. This turns ~70s suites into ~10 minutes and makes deadline tests fail under load (cases 24–26). E-1908 (submitted) covers only the sandbox destroy tests that set `HOME` themselves, not the runner.
- **Editing a script mid-run.** The runner executes `verify.sh` in place, and bash reads it incrementally, so an agent editing its suite while Mike runs it breaks Mike's run (case 29).
- **Evidence is deleted.** A later passing run clears the task's earlier failed reports, so a failure Mike saw can no longer be inspected once the agent re-runs green (case 30). E-2243 designed it this way; it costs evidence in exactly these disputes.

## Proposed bugfix tasks (for Mike to approve; none filed)

1. **Runner: build the suite env from an explicit denylist of caller identity.** Before running anything, including the sandbox reset/seed, drop `CLAUDECODE`, `CLAUDE_CODE_*`, `CLAUDE_*`, `AI_AGENT`, `__CFBundleIdentifier`, `ENDLESS_AUDIENCE`, `ENDLESS_SESSION_ID`, `TMUX`, `TMUX_PANE`. A suite that needs a harness or session sets it on the one call that needs it, as the 9 patched suites already do. This makes agent and user runs identical at the source. It also changes E-2263's premise: running in the user's context is no longer needed for *correctness*, only for land authority.
   - PRODUCT: in someone else's project, any test tool that changes behaviour under `CLAUDECODE` (the variable exists for exactly that reason) diverges the same way, so the strip is generic, not Endless-specific.
   - Design choice for you: whether `TMUX`/`TMUX_PANE` go, since a few suites test tmux behaviour. They could opt back in.
   - Front-door half: `endless task verify` should not export `ENDLESS_AUDIENCE` into the runner's environment.
   - Small; recommend filing under E-2261, ahead of E-2263.
2. **Runner: keep build caches warm across the temp HOME.** Resolve `GOCACHE`, `GOMODCACHE` and `UV_CACHE_DIR` from the caller's real `HOME` before replacing it, and pass them through explicitly. These are caches, not config, so ED-1583's isolation goal is unaffected. Small. Either file it or fold it into E-1908.
3. **Runner: execute a snapshot of `verify.sh`.** Copy it into the run dir and run the copy. A couple of lines; removes case 29's class.
4. *(Optional, lower value)* When a pass clears earlier failed reports, keep failures from runs at a different commit than the passing one, or from a different caller. My recommendation is to drop this one; 1–3 remove most of the disputes it would help settle.

## Method and limits

- Scan script: scratchpad `scan.py` (regex prefilter over 529 transcripts since 2026-07-10, user-side failure reports plus agent-side explanations). Four subagents then read the 243 candidate sessions in context. Cases where Mike described a failure without the words the regex matched may be missed.
- Reproduction emulated the user's context by stripping variables, because the tmux sibling-pane spawn was refused by the permission classifier. The table shows the variables really are absent in the "user" columns.
- Five failed probe reports remain in `~/Library/Caches/endless/verify/E-2266/`. A worktree hook blocked removing them. They are ordinary failed-run cache files and safe to delete.
