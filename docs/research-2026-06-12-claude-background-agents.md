# Claude Code Background Sessions & Agent View: Integration Reference for "endless"

## TL;DR
- Claude Code's background-agent system (branded **Agent View**, introduced in v2.1.139 on May 11, 2026, research preview) gives `endless` a clean integration surface: launch with `claude --bg "<prompt>"` (returns a short ID on stdout), discover/poll with `claude agents --json`, attach/promote to a dedicated terminal with `claude attach <id>`, and read on-disk state under `~/.claude/jobs/<id>/state.json` plus the daemon roster at `~/.claude/daemon/roster.json`.
- Viewing inside an active session (Space=peek, Enter=attach) does **not** terminate background status — sessions are hosted by a per-user supervisor (daemon) that runs them detached from any terminal; "attach" simply opens the running session in your current pane, and detaching (`←`, `Ctrl+Z`, `/exit`) leaves it running. To put a background agent in its own dedicated tmux window, run `claude attach <id>` in that window.
- The biggest integration hazards are: (1) quota scales linearly with N parallel agents — per the official docs, "running ten agents in parallel uses quota roughly ten times as fast as running one" — plus undocumented server-side burst/concurrency caps and a "thread limit"; (2) background sessions are **local only** and die on machine shutdown (recover with `claude respawn --all`); and (3) as of v2.1.139–2.1.143 the `template:"bg"` flag and `$CLAUDE_JOB_DIR` are set on *every* session including interactive ones (bug #59848), so they cannot reliably distinguish a true background agent.

## Key Findings

**1. Launch.** Three documented launch paths: `claude --bg "<prompt>"` (shell, scriptable), `/bg` or `/background` (from inside an interactive session), and typing a prompt into the `claude agents` dispatch input. `claude --bg` prints a short ID (~8 hex chars, git-hash-like) plus management commands to stdout. `--name` and `--agent` are supported.

**2. View vs. promote.** Inside a session, `←` (left arrow on empty prompt) or `/bg` backgrounds the current session and opens Agent View; Space peeks, Enter/→ attaches. Attaching is a *view that keeps the agent running*, not a promotion that kills the daemon copy. Detaching never stops it.

**3. Dedicated terminal.** `claude attach <id>` opens the running background session in the current terminal/tmux pane. The supervisor keeps hosting it; attach just connects a terminal to the already-running process.

**4. State model & discovery.** States: Working / Needs input / Idle / Completed / Failed / Stopped. Programmatic discovery: `claude agents --json`, files under `~/.claude/jobs/<id>/`, `~/.claude/daemon/roster.json`, and `claude daemon status`.

**5. SessionStart hook.** Fires for background agents; `/bg` resumes a saved conversation so it maps to `source:"resume"`, while fresh `claude --bg` is `source:"startup"`. No dedicated "background" source value or hook payload field.

**6. Daemon lifecycle.** A per-user supervisor process hosts sessions; survives terminal/shell close and auto-updates; sessions survive sleep (v2.1.142+) but die on shutdown.

**7. Concurrency & quota.** Linear quota burn (10×); soft "thread limit" cap and server-side burst limiter exist.

**8. Version & stability.** Introduced v2.1.139 (May 11, 2026); research preview; multiple open issues.

## Details

### 1. Launch mechanics

**Invocation.** Official docs document all three entry points. From the shell (the scriptable path for `endless`):

```
claude --bg "investigate the flaky SettingsChangeDetector test"
claude --agent code-reviewer --bg "address review comments on PR 1234"
claude --bg --name "flaky-test-fix" "investigate the flaky SettingsChangeDetector test"
```

`/bg` (alias of `/background`) backgrounds the current interactive session; you can append a final instruction (`/bg run the test suite and fix any failures`). Typing into the `claude agents` dispatch input launches a new background session per prompt.

**ID return form.** After `claude --bg`, Claude prints the session's short ID and a management command block to **stdout**. Documented example:

```
backgrounded · 7c5dcf5d · flaky-test-fix
  claude agents             list sessions
  claude attach 7c5dcf5d    open in this terminal
  claude logs 7c5dcf5d      show recent output
  claude stop 7c5dcf5d      stop this session
```

The short ID is also the directory name under `~/.claude/jobs/<id>/`. This is a human-formatted text block, not JSON — for machine consumption, `endless` should prefer `claude agents --json` (see §4) rather than parsing this. Note: there is a long-standing limitation that a running session cannot easily read its *own* session ID from inside (issues #25642, #44607); the `--bg` stdout ID is the reliable capture point at launch time.

**Config-flag inheritance at launch.** When backgrounding an existing session, these flags carry through to the backgrounded process: `--mcp-config`, `--strict-mcp-config`, `--settings`, `--add-dir`, `--plugin-dir`, `--fallback-model`, `--allow-dangerously-skip-permissions`, plus any `/add-dir` directories. Permission mode/model/effort persist across supervisor restarts (stored in the session's `respawnFlags`). Dispatching from the Agent View input or `claude --bg` uses the directory's `defaultMode`; `bypassPermissions`/`auto` are refused until accepted once interactively in that directory.

**Stdout/stderr semantics during launch.** `claude --bg` emits the backgrounded confirmation block to stdout and returns control to the shell immediately (it does not stream the agent's ongoing output — that goes to the supervisor-hosted session, retrievable later via `claude logs <id>`). A `--bg --exec '<cmd>'` variant runs a PTY-backed shell job instead of a model session; its captured output stays in memory (not on disk) and the row auto-cleans ~5 minutes after the command exits.

**Worktree isolation.** Every background session starts in your working directory but, before editing files, moves itself into an isolated git worktree under `.claude/worktrees/`. Disable via `worktree.bgIsolation: "none"` (v2.1.143+). Outside a git repo there is no isolation. `endless` must account for the fact that a dispatched agent's edits land in a worktree, not the main checkout, until merged/pushed.

### 2. Resume / view from inside an active session

`claude agents` (or `←` from any session) opens Agent View. Select a row, press **Space** to peek (last output or pending question, reply inline), or **Enter/→** to **attach** — Agent View is replaced by the full interactive session. Official docs: "Each background session is a full Claude Code conversation that keeps running without a terminal attached." Attaching does **not** terminate background status; it connects a terminal to the running process, and Claude posts a short recap of what happened while you were away. Detaching via `←`, `Ctrl+Z`, `/exit`, or double `Ctrl+C`/`Ctrl+D` all leave the session running; only `/stop` (or `Ctrl+X` / `claude stop`) ends it.

**Concurrent viewing.** Multiple terminals can peek/inspect via `claude logs <id>` and `claude agents --json` simultaneously (read paths). The docs do not explicitly bless two *interactive attaches* to the same session at once; the model is one supervisor-hosted process that a terminal connects to. There are reports of attach instability (e.g., "claude agents attach occasionally bouncing straight back to the session list on the first try after a background-service restart," fixed in a later release). For `endless`, treat read/peek as freely concurrent and interactive attach as effectively single-consumer.

### 3. Promote to a dedicated tmux pane/terminal

The supported invocation is **`claude attach <id>`** (run it inside the target tmux window/pane). There is **no** new foreground process that replaces the daemon copy: the existing supervisor-hosted process continues running, and `claude attach` simply connects your terminal to it. The old process does **not** terminate, and detaching leaves it running. Attached sessions always render in fullscreen mode (a background session has no scrollback to append to), which affects how tmux copy-mode and native scroll behave — only the current viewport is visible to tmux.

Practical `endless` pattern: to give a chosen agent its own dedicated tmux window, `tmux new-window` (or `send-keys`) running `claude attach <id>`. `Alt+1`..`Alt+9` inside Agent View attach to sessions 1–9 in the focused directory.

### 4. State model and discovery

**State names (official).** The per-row state icon encodes one of:

| State | Icon | Meaning |
|---|---|---|
| Working | Animated | Actively running tools / generating |
| Needs input | Yellow | Waiting on a question or permission decision |
| Idle | Dimmed | Nothing to do; ready for next prompt |
| Completed | Green | Finished successfully |
| Failed | Red | Ended with an error |
| Stopped | Grey | Stopped via `Ctrl+X` or `claude stop` |

Separately, icon **shape** encodes process liveness: `✻`/animated `✽` = process alive; `∙` = process exited but resumable; `✢` = a `/loop` session sleeping between iterations. Note Agent View's *group* headers (Ready for review, Needs input, Working, Completed) don't map 1:1 to states — "Ready for review" means an open PR, and "Completed" collects finished+failed+stopped.

**Programmatic discovery (the core of `endless` integration):**

- **`claude agents --json`** — prints live sessions as a JSON array and exits. Each entry has `pid`, `cwd`, `kind`, and `startedAt`, plus `sessionId`, `name`, and `status` when set. When `status` is `waiting`, a `waitingFor` field says what it's blocked on (e.g., `permission prompt` or `input needed`). v2.1.145 added `--json`; a later release fixed it omitting blocked/just-dispatched sessions and **added `--all` (to include completed sessions) plus new `id` and `state` fields**. Combine with `--cwd <path>` to filter by project.
- **On-disk state files** (under `CLAUDE_CONFIG_DIR` or `~/.claude`):
  - `~/.claude/daemon/roster.json` — list of running background sessions (used to reconnect after restart).
  - `~/.claude/jobs/<id>/state.json` — per-session state. Community-observed fields (issue #59848, captured on v2.1.143) are, verbatim: `{ "state": "blocked", "tempo": "active", … "template": "bg", "respawnFlags": ["--effort","high","--permission-mode","auto"], "name": "push-code-changes", "nameSource": "auto", … "cliVersion": "2.1.143", "cwd": "/Users/smabe/projects/HealthData" }` (ellipses denote undisclosed fields). The schema is **not officially documented** — treat as illustrative and version-fragile.
  - `~/.claude/jobs/<id>/tmp/` — per-session scratch dir; `$CLAUDE_JOB_DIR/tmp` writes don't prompt for permission.
  - `~/.claude/daemon.log` — supervisor log.
- **`claude daemon status`** — reports whether the supervisor is reachable, its PID and version, the socket directory, and live session count. (Undocumented in the CLI reference per issue #58869, but functional; `/doctor` runs the same check.)

Each row's one-line summary is itself generated by "a Haiku-class model"; per the official docs, "While a session is actively working, the summary refreshes at most once every 15 seconds, plus once when each turn ends" — so the human-readable status text lags real-time by up to ~15s, another reason to read structured `state`/`status` fields rather than scraping summaries.

**Recommendation for `endless`:** poll `claude agents --json --all` as the primary, stable, supported discovery API; fall back to reading `roster.json` / `state.json` only for fields the JSON doesn't expose, and guard against schema drift.

### 5. SessionStart hook behavior in background sessions

**Does it fire?** Yes. SessionStart "runs on every session." The `source` field enumerates only `startup` / `resume` / `clear` / `compact`. Because backgrounding "starts a fresh process that resumes from the saved conversation," a `/bg` or `←` background maps to **`source:"resume"`** (strong inference from the documented resume mechanism + the documented "resume → `source:'resume'`" rule; *not* an explicit doc statement for `/bg`). A fresh `claude --bg "<prompt>"` with no prior conversation is a new session → **`source:"startup"`**. There is **no** dedicated `"background"`/`"bg"` source value.

**Any hook payload field that says "this is a background agent"? No.** The documented SessionStart stdin payload is `session_id`, `transcript_path`, `cwd`, `hook_event_name`, `source`, `model`, and optionally `agent_type` and `session_title`. There is no `template`, `sessionKind`, or `is_background` field in the hook JSON. `agent_id`/`agent_type` identify *subagents*, not daemon-hosted background sessions.

**Indirect detection — and why it's unreliable.** A hook process inherits the parent environment, so it *could* read `$CLAUDE_JOB_DIR` or `cat $CLAUDE_JOB_DIR/state.json` to check `template`. But issue #59848 documents that, post-v2.1.139, **every** session — including a plain interactive `claude` — gets `$CLAUDE_JOB_DIR`, a daemon-managed state file, and `template:"bg"`. So as of v2.1.139–2.1.143, neither `$CLAUDE_JOB_DIR` presence nor `template:"bg"` reliably distinguishes a real background agent. The proposed fix (reserve `template:"bg"` for genuinely spawned sessions, set `template:"foreground"` otherwise) is *proposed, not shipped* in those versions. `endless` should therefore identify background agents via `claude agents --json` membership rather than via hook-side environment sniffing.

**Environment inheritance for hook context.** `CLAUDE_JOB_DIR` is set to `~/.claude/jobs/<id>` for each background session (documented). `CLAUDECODE=1` is set in subprocesses Claude Code spawns (Bash/PowerShell tools, tmux sessions, hook commands, statusline, stdio MCP servers). `CLAUDE_ENV_FILE` is available in SessionStart/Setup/CwdChanged/FileChanged hooks to persist env vars for the session. The per-user supervisor persists independently with its own long-lived environment rather than cleanly inheriting each spawning shell — confirmed by environment-parity bugs where the bg boot path drops user agents (#58729) and Claude.ai connectors (#58353) that interactive sessions load. (`CLAUDE_CODE_ENTRYPOINT` is a known internal launch-tag variable but was not confirmed in the env-vars docs or the bg env dumps reviewed; treat its bg value as unverified.) Caution: a SessionStart hook that spawns a backgrounded child holding stdin/stdout can deadlock the session (issue #43123, after v2.1.87 tightened subprocess comms) — relevant because hook execution context changed in the same v2.1.139 release (command hooks now run without terminal access).

**Known SessionStart reliability bugs:** historically (issue #10373) SessionStart hook stdout was not injected into context for brand-new interactive sessions in some builds (worked for `/clear`, `/compact`, URL-resume). On the VSCode extension, `/clear` reported `source:"startup"` instead of `"clear"` (#49937). `endless` should not assume the `source` value is perfectly reliable across surfaces.

### 6. Daemon lifecycle

A **per-user supervisor process** hosts every background session, separate from your terminal and Agent View. It starts automatically the first time you background a session or open Agent View, and exits on its own when no sessions remain and no terminal is connected. It is keyed to `CLAUDE_CONFIG_DIR` (setting that runs a separate supervisor instance with its own sessions). The supervisor persists independently with its own long-lived environment rather than cleanly inheriting each spawning shell — confirmed by the environment-parity bugs noted above.

**Effects of machine events:**
- **Closing the spawning terminal / shell:** no effect — sessions keep running (detached, supervisor-hosted).
- **Machine sleep:** preserved. Per the official docs, "Sessions are also preserved when your machine sleeps. Their processes resume on wake and the supervisor reconnects to them instead of treating the time gap as idle." v2.1.142+ the daemon detects clock jumps to do this. (The agent-view doc page was reported temporarily outdated on this point — issue #59263.)
- **Machine shutdown / restart:** Per the official docs, "Shutting down still stops running sessions." Running sessions show as **Failed** on next open. Recovery: attach/peek/reply to restart from where it left off, or `claude respawn --all` to restart all at once.
- **Idle reaping:** Per the official docs and field reports, "After a session has been idle and unattached for about an hour, the supervisor stops the underlying process to free resources — peek or attach and Claude restarts it from where it left off." State persists on disk. Pinned sessions (`Ctrl+T`) are exempt and restart in place across updates.
- **Auto-update:** supervisor watches the binary on disk (local file watch, not network) and restarts into the new version; detached sessions survive; `claude respawn <id>` / `--all` moves sessions onto a new binary.

**Recovery semantics:** respawn/restart **resumes** the saved conversation/state (not a clean start). Transcript lives at `~/.claude/projects/<encoded-cwd>/<full-session-id>.jsonl` and remains resumable via `claude --resume <full-id>` even after a job is deleted from Agent View (#58725).

**Recovery command for `endless`:** if attach/peek/`claude logs` reports "background service did not respond," the supervisor stalled — `claude daemon stop --any --keep-workers` restarts the supervisor while keeping sessions alive.

### 7. Concurrency and quota

**Quota.** Official limitation, verbatim from the Agent View docs: "Rate limits apply: background sessions consume your subscription usage the same as interactive sessions, so running ten agents in parallel uses quota roughly ten times as fast as running one." There is **no separate agent billing**. Each row summary is also a small Haiku-class request (refreshes at most every 15s plus once per turn-end), billed to your quota.

**Soft caps / throttling (community-observed):**
- A **"thread limit"** soft ceiling on concurrent session dispatch (reported: "One QA/accessibility reviewer did not spawn because the thread limit is reached"). This is a concurrency cap distinct from the rate-limit quota.
- A **server-side burst/concurrency limiter** that fires when many sessions bootstrap simultaneously (issue #53922: first 3–4 of ~10 rapid-fire sessions succeed, the rest get "Server is temporarily limiting requests (not your usage limit) · Rate limited" until retried with delay).
- **Per-model throughput (RPM/TPM)** limits bite hardest on Opus; multiple parallel Opus sessions hit throughput caps well before the usage cap.
- Supervisor switching/deadlock pauses (30–60s) reported when rapidly switching between freshly dispatched sessions.
- There is **no** user-facing `maxParallelAgents` setting (requested in #15487); Anthropic does not publish a hard cap on simultaneous sessions.

**Practical ceilings (community guidance).** The FindSkill.ai "Week 1 Failure Modes" field report concludes that "Three to five parallel sessions is the sweet spot for most users," with ~2 the realistic ceiling on Pro before hitting thread limits, switching deadlocks, and supervisor pauses. `endless` should implement its own dispatch throttling (stagger launches, exponential backoff with jitter on the "temporarily limiting requests" error) rather than relying on Claude Code to queue.

### 8. Version availability and stability

**Introduction.** Agent View shipped in **Claude Code v2.1.139**, announced by Anthropic on **May 11, 2026** (the GitHub CHANGELOG entry is dated May 11–12). `claude --bg`, `/bg` (alias `/background`), and `claude agents` all arrived with this release; `/goal` shipped in the same version. Background *agents* (the underlying send-to-background + `/tasks` capability) predate Agent View (community references to v2.0.60). Availability: research preview on **Pro, Max, Team, Enterprise, and Claude API** plans (note: one report — issue #58570 — found Agent View *not* enabled for some Claude API users on v2.1.140 despite the docs; verify per-account). Free plan: interactive only, no background agents.

**Version progression of relevant capabilities:**
- v2.1.139 — Agent View + `claude agents` + `/bg` + `--bg` (research preview).
- v2.1.141 — `claude agents --cwd <path>` scoping; `claude daemon status` referenced.
- v2.1.142 — dispatch flags (`--permission-mode`, `--model`, `--effort`, `--dangerously-skip-permissions`); macOS sleep/wake reconnect fix.
- v2.1.143 — `--allow-dangerously-skip-permissions`; `worktree.bgIsolation` setting.
- v2.1.144 — `/resume` support for background sessions (marked with `bg`).
- v2.1.145 — `claude agents --json`; awaiting-input tab counts.
- v2.1.147 — pinned background sessions stay alive when idle, restart in place for updates.
- v2.1.157 — `--agent` honored for dispatched sessions.
- A later release — `claude agents --json` `--all`/`id`/`state` fields fix.

**Current support.** As of this report (June 12, 2026), the feature is shipping. Per the LaoZhang AI blog, "As of May 19, 2026, the npm latest tag for @anthropic-ai/claude-code was 2.1.143"; subsequently v2.1.162 released June 3, with v2.1.168/2.1.169 referenced in later changelogs. Use `claude --version` and confirm `claude agents` opens the dashboard rather than listing subagents.

**Known regressions / open issues (stability):**
- **#59848** — interactive sessions misclassified as background (`template:"bg"`, `$CLAUDE_JOB_DIR` on foreground sessions), triggering bg-only worktree-isolation guards on interactive edits. *Most important for `endless`'s detection logic.*
- **#58729** — background sessions don't load user agents from `~/.claude/agents/` into the Agent tool registry.
- **#58570** — Agent View not enabled for some Claude API users on v2.1.140.
- **#58725** — `/resume` picker omits background sessions.
- **#59263** — agent-view sleep/wake docs outdated for v2.1.142.
- **#58869** — `claude daemon status` undocumented.
- **#43123** — SessionStart hook with a backgrounded child process deadlocks the session.
- **#65971** — conversational mention of "workflow" can trigger dynamic workflows and leave a persistent daemon that hijacks `claude` into Agent View.
- Various TUI/IDE attach and keyboard-focus bugs (WSL frame corruption; VSCode agents-manager focus loss on 2.1.168, #66015).
- Documented fixes show active churn: EAUTH on attach after daemon auto-update, busy-spinner lag, stale frames, truncated status text, image-paste no-ops.

Because the feature is explicitly **research preview**, Anthropic warns the interface and keyboard shortcuts may change. `endless` should pin to the stable surfaces (CLI subcommands and `--json`) and avoid depending on TUI keybindings or undocumented `state.json` fields.

## Recommendations

**Stage 1 — Minimal viable integration (depend only on documented, stable surfaces).**
1. **Dispatch** child tasks with `claude --bg --name "<endless-task-id>" "<prompt>"` (optionally `--agent`), and **capture the short ID from stdout** at launch (parse the `backgrounded · <id> · <name>` line). Setting `--name` to your own task ID makes the row identifiable in `--json` output.
2. **Discover/poll** with `claude agents --json --all` (filter by `--cwd` when you want one project). Map its `state`/`status`/`waitingFor` fields to your task board. Poll on an interval (e.g., 5–15s) rather than tailing files; remember row summaries lag up to ~15s, so key your logic on structured state, not summary text.
3. **Promote to a dedicated tmux window** by opening a new window/pane running `claude attach <id>`. Don't expect this to change the agent's lifecycle — the supervisor keeps hosting it, and detaching leaves it running.
4. **Stop/clean up** with `claude stop <id>` and `claude rm <id>` (remember `claude rm` keeps worktrees with uncommitted changes and prints their path; deleting in the TUI via `Ctrl+X Ctrl+X` discards the worktree).

**Stage 2 — Robustness.**
5. **Throttle your own dispatch**: cap concurrent launches (start at ≤3, configurable; 3–5 is the community sweet spot), stagger them, and implement exponential backoff with jitter on "Server is temporarily limiting requests" and 429/529. Do not assume Claude Code queues for you.
6. **Pin** long-running monitor agents (`Ctrl+T` semantics) or keep them active; otherwise expect ~1h idle reaping and a cold-start delay on next attach.
7. **Handle shutdown/sleep**: on host restart, detect Failed sessions and offer `claude respawn --all`; treat sleep as recoverable on v2.1.142+.
8. **Daemon health**: surface `claude daemon status`; on "background service did not respond," run `claude daemon stop --any --keep-workers`.

**Stage 3 — Advanced / fragile (guard behind capability checks).**
9. Read `~/.claude/jobs/<id>/state.json` and `roster.json` only for fields `--json` doesn't expose, and version-guard against schema drift.
10. For hook-based eventing, register a SessionStart hook but **do not** rely on it to distinguish background vs. foreground (the `template`/`$CLAUDE_JOB_DIR` signals are unreliable through v2.1.143). Identify background agents via `claude agents --json` membership instead.

**Thresholds that change the plan:**
- If Anthropic ships the #59848 fix (reliable `template:"foreground"` vs `"bg"`), hook-side background detection becomes viable — revisit Stage 3.10.
- If a documented `maxParallelAgents`/queueing setting ships (#15487), you can relax your own throttling (Stage 2.5).
- If Agent View exits research preview, the `--json` schema and TUI keybindings stabilize enough to depend on more directly.
- If a cloud/remote supervisor (cross-machine roster sync) ships, revisit the "local only / dies on shutdown" assumption.

## Caveats
- **Research preview**: interface, shortcuts, state names, and on-disk schema may change without notice; disableable via the `disableAgentView` setting or `CLAUDE_CODE_DISABLE_AGENT_VIEW` environment variable (administrators can enforce via managed settings).
- **Inference flags**: the exact SessionStart `source` value for `/bg` (resume) vs `claude --bg` (startup) is a strong inference from documented mechanisms, not an explicit doc statement.
- **Community vs official**: state-icon names, `claude agents --json` field names, the supervisor model, `CLAUDE_JOB_DIR`, the Haiku summary cadence, and the 10× quota multiplier are from **official docs**; the `state.json` field list, "thread limit," burst limiter, switching deadlock, and practical 3–5 parallel ceiling are **community/issue-reported** and version-fragile.
- **`claude daemon status` and the full `state.json` schema are undocumented**; do not treat as a stable API.
- **Local only**: background sessions are not cloud-synced; there is no cross-machine roster. For cross-machine work, Claude Code on the web is a separate product that runs sessions in Anthropic's cloud and survives machine sleep.
- **Worktree side effects**: dispatched agents edit in `.claude/worktrees/`, and deleting a session in Agent View deletes its worktree (including uncommitted changes) — `endless` must surface "merge/push before delete."
- **Account variance**: some Claude API accounts reportedly lacked Agent View even on a qualifying version (#58570); always verify with `claude agents` at runtime.

### Source URLs
- Official: https://code.claude.com/docs/en/agent-view · https://code.claude.com/docs/en/hooks · https://code.claude.com/docs/en/env-vars · https://code.claude.com/docs/en/cli-reference · https://code.claude.com/docs/en/agents · https://code.claude.com/docs/en/whats-new · https://code.claude.com/docs/en/changelog · https://code.claude.com/docs/en/agent-sdk/sessions · https://claude.com/blog/agent-view-in-claude-code
- GitHub (anthropics/claude-code): CHANGELOG https://github.com/anthropics/claude-code/blob/main/CHANGELOG.md · Releases https://github.com/anthropics/claude-code/releases · Issues #59848, #58729, #58570, #58725, #59263, #58869, #43123, #65971, #66015, #53922, #15487, #10373, #49937, #25642, #44607
- Community: https://www.dsebastien.net/claude-code-agent-view/ · https://pasqualepillitteri.it/en/news/2384/claude-code-agent-view-cli-dashboard-sessions-2026 · https://findskill.ai/blog/claude-code-10-parallel-agents-week-1/ · https://www.cloudzero.com/blog/claude-code-agents/ · https://blog.laozhang.ai/en/posts/claude-code-agent-view · https://changelogs.directory/tools/claude-code/releases/2.1.139 · https://wmedia.es/en/tips/claude-code-agent-view-parallel-sessions · https://developertoolkit.ai/en/claude-code/advanced-techniques/agent-view/