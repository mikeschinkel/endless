# Refusal and warning inventory — which ones an agent reports (E-2155)

Every user-facing refusal and warning in Endless, Python and Go, classified by
whether an agent must report it to the user. The rows are in
[`research-2026-09-17-refusal-inventory.tsv`](research-2026-09-17-refusal-inventory.tsv);
this page is the method, the totals, the REPORT list, the enforcement design, and
what did not fit.

Line numbers are as of commit `ca327f109`. They go stale as the code moves; the
`message` column is the stable way to find a site again.

## The rule

> An agent reports a refusal or warning to the user **only when it cannot continue
> without input from the user.** Everything else is NO-REPORT. "You must do X
> instead of Y" is NO-REPORT: the agent calls the command again doing X.

Classified for PRODUCT: an agent on someone else's machine, in a project that is
not Endless. "Run `just install`" is not a remedy there.

## Method

Eleven read-only audits ran in parallel, one per slice of the source, against one
written brief carrying the rule, the owner's calibration examples, and a fixed
TSV schema. Each audit read the code around every site rather than classifying
from the message string. Afterwards every REPORT row and every CONDITIONAL row
was re-read for consistency across slices, and the Python candidate sites
(every `ClickException`/`UsageError` construction, `err=True` echo, stderr
write and non-zero exit) were cross-checked against the rows: all are covered,
several through the row for the helper that builds the message.

`tension:` notes and side findings in the TSV are the auditors' readings. The
ones promoted into this page's *message defects* and *defects found* sections
were re-verified against the code.

A row is one message-construction site. A helper that builds a message raised
from several places is one row, with the raise sites in `notes`. An echo
followed by an exit is one row citing both lines.

### Columns

| Column | Meaning |
|---|---|
| `site` | `path:line`, `path:line+line` for a pair |
| `lang` | `py` or `go` |
| `construct` | raise, usage, echo-err, exit, stderr, hook-block, hook-json, hook-context, log, error-return, helper |
| `audience` | `cli` (whoever ran `endless …`), `hook-agent` (Claude Code feeds it to the model), `hook-user` (hook stderr the human sees), `internal` (Go output captured and relayed by Python), `tmux`, `job-log`, `none` |
| `class` | REPORT, NO-REPORT, CONDITIONAL — the inventory. INFO and EXCLUDED — counted, not classified (below) |
| `kind` | validation, usage, not-found, state-guard, policy-guard, user-act, confirmation, gate, environment, fault, degraded, relay, idempotent, other |
| `remedy_or_decision_or_condition` | NO-REPORT: what the agent does instead. REPORT: the decision only the user can make. CONDITIONAL: `REPORT when …; NO-REPORT when …`. INFO/EXCLUDED: why |
| `notes` | `tension:` where the rule gives an answer that looks wrong, callers, side findings |

## Totals

1136 rows. **981 are the inventory** (REPORT + NO-REPORT + CONDITIONAL); 155 are
stderr output that is not a refusal or reaches nobody.

| Class | Python | Go | Total |
|---|---:|---:|---:|
| NO-REPORT | 338 | 249 | **587** |
| REPORT | 80 | 120 | **200** |
| CONDITIONAL | 114 | 80 | **194** |
| *inventory* | *532* | *449* | ***981*** |
| INFO — stderr that is not a refusal or warning | 12 | 15 | 27 |
| EXCLUDED — reaches no human and no agent | 24 | 104 | 128 |

587 + 200 + 194 = 981.

NO-REPORT by kind: usage 167, validation 155, not-found 90, degraded 79,
policy-guard 26, idempotent 24, state-guard 21, environment 11, other 8, fault 5,
gate 1.

Audience of the 981: `cli` 758, `internal` 168, `hook-agent` 20, `hook-user` 15,
`tmux` 14, `job-log` 6.

## REPORT — 200 rows, but only 35 name a decision

The plan expected REPORT to be short. It is not, and the reason is structural:
**110 REPORT rows are faults** (Endless itself failed) and **55 are environment**
(something only the user can install or fix). Under the rule both are REPORT,
since the agent cannot continue, but neither names a decision in the sense the
plan meant. The REPORT rows that do name one number **35**.

### Decision REPORTs (35)

| Site | Message | The decision only the user can make |
|---|---|---|
| `src/endless/config.py:295` | content.extensions lists `<ext>` under both block and unblock | whether `<ext>` is blocked or unblocked — the two lists contradict each other |
| `src/endless/db_restore.py:310` | no backups found in `<dir>` | which backup, if any, to restore from |
| `src/endless/db_restore.py:513` | refusing to restore: `<n>` processes have the database open | stop the user's own sessions/monitors, or accept `--force` |
| `src/endless/lesson_cmd.py:189` | git commit failed for LESSONS.md; lesson is written | how the uncommitted lesson gets committed on main |
| `src/endless/session_cmd.py:209` | Claude transcript is gone | recover the transcript, or abandon the conversation (irreversible) |
| `src/endless/session_cmd.py:429` | task is declined/obsolete, a deliberate decision (resume) | whether to revive a task the user declined or retired |
| `src/endless/session_cmd.py:2647` | task is declined/obsolete, a deliberate decision (goto) | same as :429 |
| `src/endless/task_cmd.py:4505` | task already active in another live session | continue in the owning session, or end it |
| `src/endless/task_cmd.py:4686` | task was claimed by session ES-n; pick it up there | resume the prior session (moves the user's tmux focus) |
| `src/endless/task_cmd.py:4919` | re-claiming would demote settled work to underway | whether to reopen settled work — every reopen edge is the user's |
| `src/endless/task_cmd.py:5298` | task is declined/obsolete; reverse that decision explicitly | whether to reverse a decline or retirement |
| `src/endless/task_cmd.py:6924` | settled work; pick it back up with goto --revisit | whether to reopen shipped work |
| `src/endless/task_cmd.py:6935` | spawning would demote settled work to underway | whether to reopen settled work |
| `src/endless/unregister.py:86` | Aborted! at "delete .endless and ignore?" | whether to delete the project's `.endless/` — destructive |
| `src/endless/worktree_cmd.py:1689` | orphan branch has commits touching non-plan files | keep that work, or discard it (`branch -D` destroys commits) |
| `src/endless/worktree_cmd.py:1856` | path exists but does not belong to this task (claim) | what to do with a directory the agent did not create |
| `src/endless/worktree_cmd.py:1970` | same, on `session resume --review/--reopen` | same as :1856 |
| `src/endless/worktree_cmd.py:2245` | land un-ignored `<n>` paths, left untracked on main | delete, track or re-ignore what may be the user's own local files |
| `src/endless/worktree_cmd.py:3563` | cannot verify in-use: endless-go not on PATH | worktree removal is the user's act |
| `src/endless/worktree_cmd.py:3569` | cannot verify in-use: `<detail>` | worktree removal is the user's act |
| `src/endless/worktree_cmd.py:3574` | refusing to drop a worktree that is in use | whether to remove a worktree a live session or process uses |
| `src/endless/worktree_cmd.py:3608` | refusing to drop the main checkout | what the user actually meant |
| `src/endless/worktree_cmd.py:3614` | refusing to drop a foreign worktree | whether to remove a worktree Endless did not create |
| `src/endless/worktree_cmd.py:3632` | worktree has uncommitted changes | removal would destroy uncommitted work |
| `src/endless/worktree_cmd.py:3637` | git status check failed (drop) | removal is the user's act; its safety check failed |
| `src/endless/worktree_cmd.py:3646` | git worktree remove failed | removal is the user's act |
| `internal/monitor/worktree_inuse.go:19+23+26` | reason strings quoted by the drop refusal | stop that session/process, or force the drop |
| `internal/hookcmd/claude.go:372+2014` | this worktree is owned by session `<s>` (SessionStart) | start this session elsewhere, or end the owner |
| `internal/hookcmd/claude.go:997+1050` | BLOCKED: session is in `needs_input` | answer the question the session asked |
| `internal/hookcmd/claude.go:997+1055` | BLOCKED: session is in `<state>`; end the turn | none — see *did not fit* |
| `internal/hookcmd/claude.go:1201+1202` | epic set to revisit: ask continue or stop | continue under the current plan, or stop |
| `internal/hookcmd/claude.go:1425` | BLOCKED: refusing to remove a worktree | worktree removal is the user's act |
| `internal/hookcmd/claude.go:2161+2169` | cwd is not the claimed task's worktree: `/cd` into it | the user types `/cd` — the model cannot run it |
| `internal/projectstatuscmd/window.go:258+263` | tmux session already holds another project's monitor | pick a per-project `tmux.session_name`, or close that monitor |
| `internal/projectstatuscmd/window.go:265+270` | a tmux session with that name exists and Endless did not create it | close their own tmux session, or rename ours |

### Environment REPORTs (55)

The user must install, start or repair something. Grouped by file:

- `src/endless/`: cli.py:2705 · event_bridge.py:73 · project_path.py:69 ·
  project_status_cmd.py:47 · report_cmd.py:183 · session_cmd.py:127, 228, 307,
  1076, 2722, 2810, 2847 · session_states.py:92 · setup.py:38, 471, 624 ·
  statuses.py:100, 208 · task_cmd.py:2270, 6869, 6871 · tmux_cmd.py:26 ·
  worktree_cmd.py:541, 1526, 2300, 2414, 2523, 2610
- `internal/`, `cmd/`: endless-migrate/main.go:119 · eventcmd/event.go:139 ·
  events/commit.go:140 · minimizerjob/minimizerjob.go:127 ·
  monitor/default_branch.go:158 · monitor/project_path.go:37 ·
  monitor/reap_worktrees.go:454 · monitor/unlanded_cache.go:539 ·
  monitor/unlanded_refresh.go:180 · monitor/worktree_unsettled.go:462 ·
  projectstatuscmd/window.go:223 · sandboxcmd/destroy.go:37, 64 ·
  sandboxcmd/enter.go:72 · sandboxcmd/init.go:50, 59 · sandboxcmd/list.go:47 ·
  sandboxcmd/prune.go:37 · sessionstatecmd/sessionstatecmd.go:95 ·
  sessionstatuscmd/session_status.go:181 · spawnlaunchcmd/tmux_driver.go:67 ·
  taskstatuscmd/taskstatuscmd.go:94 · tmuxcmd/apply.go:25 · tmuxcmd/init.go:35 ·
  triagejob/triagejob.go:122 · verifycmd/env.go:74 · verifycmd/report.go:45

Recurring causes, by message keyword (overlapping): `endless-go` missing (14),
tmux missing or not entered (9), sandbox cache directory errors (7), default
branch unresolvable or misconfigured (5), `$HOME` (3), Python/Go version skew (2).

### Fault REPORTs (110)

Endless itself failed: invariant breaks, unreadable subprocess output, DB and
schema integrity, git plumbing that should not fail.

- `src/endless/`: cli.py:986, 1002, 1080 · db_restore.py:413, 424 · db.py:524,
  721 · decision_cmd.py:83 · land_conflict.py:778 · minimizer_cmd.py:287 ·
  session_cmd.py:136, 142, 318, 2120, 2735 · session_order_cmd.py:60 ·
  session_states.py:122 · session_status_cmd.py:64 · session_task_cmd.py:79 ·
  setup.py:632 · task_cmd.py:2285 · worktree_cmd.py:129, 1250, 1401, 3322, 3434
- `internal/`, `cmd/`: endless-migrate/main.go:138, 231, 240, 247 ·
  errorscmd/errors.go:137, 159, 172, 202, 330, 412 · eventcmd/event.go:234, 834 ·
  events/commit.go:171, 315 · events/event.go:404 · events/executor.go:356 ·
  events/orphans.go:105 · events/parent_cycle.go:60 · faultbadge/faultbadge.go:123 ·
  faults/codes.go:290 · hookcmd/hook.go:55 (three audiences, three rows) ·
  jobs/jobs.go:159, 167 · jobs/run.go:87, 385, 428, 445 · jobscmd/jobs.go:59, 144 ·
  kairos/nodeid.go:27 · kairos/timestamp.go:40 · monitor/db.go:775, 828, 838,
  848, 858 · monitor/worktree_unsettled.go:435, 505 ·
  outputstylecmd/outputstyle.go:112 · projectstatuscmd/project_status.go:259 ·
  projectstatuscmd/window.go:274, 293, 309 · schemachange/errors.go:21 ·
  sessionquerycmd/session_query.go:54, 61 · sessionstatecmd/sessionstatecmd.go:106,
  116, 125 · sessionstatuscmd/session_status.go:307, 311, 329, 334, 338, 363, 369,
  566 · spawnlaunchcmd/spawn_window.go:67, 73, 81, 96 ·
  taskstatuscmd/taskstatuscmd.go:105, 115, 124 · templatecmd/template.go:243, 451 ·
  tmuxcmd/active_id.go:37 · tmuxcmd/apply.go:45 · tmuxcmd/init.go:50, 76 ·
  unlandedjob/unlandedjob.go:139 · verify/ctrf.go:130 · verify/discover.go:75, 128 ·
  verify/driver.go:179 · verifycmd/env.go:27, 98, 170 · verifycmd/script.go:205 ·
  worktreecmd/worktree.go:82, 86, 120

(Some sites carry several rows — one per message or per audience — so the site
list is shorter than 110.)

## CONDITIONAL — 194 rows, and what was decided for each

The class depends on something the message alone does not fix. Where that
something sits decides the mechanism (all settled 2026-09-18):

- **139 the site or its source already answers.** ~91 are relays — the site
  prints another process's output and inherits its class, so the class must
  travel across the process boundary (Go renders its own directive; Python's
  relay adds none). ~48 follow from state the site holds: the task's status and
  type, `ErrNoProject` versus a database error, EPIPE versus a failed write.
  These resolve to REPORT or NO-REPORT at runtime.
- **12 the command can resolve, and should.** Not "it depends" at all, once you
  use what Endless already detects: the actor (the environment says agent or
  human), the call path (Endless computed the value versus a caller passed it),
  process ancestry (whether the processes holding a sandbox are the agent's
  own), and state in hand (a `session resume --review` tree). Two worth naming:
  the live-session ambiguity refusals can resolve the session from
  `CLAUDE_CODE_SESSION_ID` rather than asking, and the interactive-prompt "file
  not found" refusals know the path came from whoever ran the command.
- **29 render as `report_if`.** These turn on what the user asked the agent to
  do — was the subtree meant to go, was reopening settled work requested — or,
  for a few, on who made an earlier change where nothing recorded it. When a
  directive renders at all the environment has already said an agent ran the
  command; what it cannot say is whether the user asked for it, and that lives
  only in the conversation. So the refusal names both branches and the
  consequence, and the agent — which holds the conversation — decides.
- **14 stop being conditional.** They belong to commands that are the user's
  alone, which refuse an agent outright (E-2162), so the message is REPORT.

## Not in the inventory, but not out of scope for enforcement

- **INFO (27)**: stderr that is not a refusal — progress lines, a hook script's
  streamed output, status notes on commands whose stdout is `eval`'d.
- **EXCLUDED (128)**: output nobody receives — 39 `log.Printf` lines in hookcmd
  (the standard logger goes to stderr and `hook.log`; Claude Code discards a
  successful hook's stderr), tmux status-line stderr, background-job logs,
  exceptions caught and handled internally.

Enforcement still has to see both. A build check cannot tell a refusal from a
progress line by looking at the write, so every stderr write has to declare
which it is — including "this is not a refusal".

## Enforcement design

### Principle

The class is part of the **name of the call**, not an argument that could be left
off. A site cannot construct a refusal without choosing one, and a test fails the
build on any stderr write or refusal construction that bypasses the helper.

### Python

**Helper.** Extend `agent_help.agent_error` (E-2097) rather than adding a
parallel one — that module has been consolidated twice already, and the bracket
it renders is where the directive belongs. A `Refusal(click.ClickException)`
with no public constructor, only class-named factories:

```python
Refusal.no_report(summary, *, remedy, guidance="")
Refusal.report(summary, *, decision, guidance="", human_remedy="")
Refusal.report_if(summary, *, condition, remedy, decision, guidance="")
Refusal.fault(summary, *, guidance="")
Refusal.relay(stderr_text)            # endless-go output: its class travels with it
Refusal.relay_foreign(text, *, as_)   # git, hook scripts: the site picks the class

warn.no_report(summary, *, remedy="")
warn.report(summary, *, decision)
info(text)                            # stderr that is not a refusal
passthrough_exit(code)                # a child's exit code, relayed
```

A state-dependent site writes both branches:
`raise (Refusal.report(...) if settled else Refusal.no_report(...))`.

`summary` is the E-2097 one-line verdict, required. Most existing refusals are a
single line today, so for them `summary` is the message and `guidance` is empty.

**Build check** (`tests/test_refusal_sites.py`, an AST walk of `src/endless/`,
precedent `tests/test_no_self_dev_ids.py`). Fails on, anywhere outside the helper
module:

- a `click.ClickException(`, `click.UsageError(`, `click.BadParameter(` or
  `Refusal(` call, or a class subclassing any of them;
- `click.echo(..., err=True)`, `print(..., file=sys.stderr)`,
  `sys.stderr.write(`;
- `sys.exit(<non-zero>)`, `raise SystemExit(<non-zero or str>)`,
  `ctx.exit(<non-zero>)`.

The failure message states the rule and lists the factories, the way
`test_no_self_dev_ids` states its rule at the moment it fires.

**Two categories Endless does not write site by site**, each classified once, in
the root group's `main`, and named in the test:

- Click's own usage errors (missing argument, unknown option, bad choice) and
  `ParamType.fail()` inside `convert()` — NO-REPORT usage by definition: fix the
  invocation.
- An uncaught exception (a traceback) — a fault.

### Go

Direct `os.Stderr` references: **308 in 45 files**; stderr writers passed as
parameters: 3. So the check is simple and nearly airtight.

**Helper.** One package (name the implementer's call; `internal/refusal` below)
mirroring the Python factories:

```go
refusal.NoReport(summary, remedy string) *Error
refusal.Report(summary, decision string) *Error
refusal.ReportIf(summary, condition, remedy, decision string) *Error
refusal.Fault(err error) *Error
refusal.Warn(class Class, summary string)
refusal.Info(text string)
refusal.Passthrough() io.Writer      // exec.Cmd.Stderr for a child's own output
(*Error).Exit(code int)              // render for the audience, write, exit
refusal.Block(*Error)                // PreToolUse: exit 2, always agent audience
```

**Build check** (a Go test using `go/parser`, walking non-test files under
`internal/` and `cmd/`). Fails on any `os.Stderr` selector outside the package —
which covers `fmt.Fprint*(os.Stderr`, `cmd.Stderr = os.Stderr`,
`log.SetOutput(os.Stderr…)` and `slog.NewTextHandler(os.Stderr`. The 3 injected
writers become `refusal`'s own type, which has no `Write` method, so an
unclassified write through one does not compile.

**Logging.** `hookcmd`'s `init()` points the standard logger at stderr and
`hook.log` in every endless-go process, not just `hook`. Logging moves under the
package and goes to the file only; `log.Printf` stops being a stderr channel.

**Error values at a generic print site.** A Run function that returns
`fmt.Errorf("--foo requires --bar")` to a `Fprintf(os.Stderr, "%v")` print site
is a refusal the static check cannot see. The print site becomes
`refusal.From(err).Exit(n)`: a `*refusal.Error` in the chain renders with its
class, anything else renders as a fault. The inventory lists every deliberately
worded error return (construct `error-return`), and each converts to a factory.
**This fallback is the one place a class is implied rather than written** — see
open question 3.

### Hooks

Hook output has a fixed audience per event and exit path, so the hook helper
takes it from there rather than from the environment:

- PreToolUse exit 2 stderr, block/deny reason, additionalContext → agent, always
  rendered with the directive.
- UserPromptSubmit/SessionStart exit 2, Stop exit 1 → the human reads it
  (`hook.go:55`'s halt notice is currently addressed to the agent on every path,
  and on UserPromptSubmit also erases the user's typed prompt).
- Exit 0 stderr and async events → nobody. Nothing user-facing goes there.

### Rendering

Humans see today's text, unchanged. For an agent, the directive sits **inside
the E-2097 verdict line**, which is repeated as the first and last line, so a
truncating pipe keeps it at either end:

```
NO-REPORT  [Endless] task add: title 107>100 chars. Nothing was created. Move the long form to --analysis and retry. Handle this yourself: do not mention this refusal to the user, now or in your summary.
REPORT     [Endless] worktree drop: E-101's worktree is in use by a live session. This needs the user: whether to remove a worktree a live session is using. Stop and ask; do not work around it.
REPORT-IF  [Endless] task remove: E-101 has 3 children. If the user did not ask for the subtree to go, stop and ask; otherwise retry with --cascade and do not mention this refusal.
FAULT      [Endless] session list: endless-go returned no output. Endless itself failed: tell the user, and do not retry more than once.
```

"now or in your summary" is deliberate: the failure that opened this task was a
refusal the agent handled correctly and then narrated in its handoff.

A REPORT for an agent carries the decision and **not** the bypass. Today the
drop refusals offer `--force` and `git worktree remove`, the removal hook offers
`reset --hard main`, and the declined-task refusals offer the status change that
reverses the user's decision (*did not fit*, item 3); `human_remedy` renders for
humans only.

The verdict format must be byte-identical across the two languages: a golden file
under the Go package, asserted by a Go test and a Python test — the pattern
`tests/test_status_lifecycle_sync.py` already uses.

### When the directive fires

1. an agent is detected in the environment (`agent_env.present()` /
   `agentenv.Present()`);
2. `--agent` was passed;
3. `--agent-view` was passed;
4. `--format agent` was passed — the long form of `--agent` (E-1504), settled
   2026-09-18, so two spellings of one flag cannot behave differently.

Harness detection itself is deliberately NOT widened (settled 2026-09-18): an
agent under a harness Endless does not recognise reads as a human, as today.

`agent_help.agent_facing()` does not honour trigger 2 today; the argv pre-scan in
the root group's `main` that already consumes `--agent-view` is the one place to
add it.

### Cross-process

Python runs endless-go as a subprocess, and Go cannot see a flag passed to
Python. When `agent_facing()` is true, the root `main` sets
`ENDLESS_AUDIENCE=agent` in the environment; Go's audience check is
`agentenv.Present() || os.Getenv("ENDLESS_AUDIENCE") == "agent"`. That also gives
Go its reading of `--agent` before E-1063 ports the flag itself.

Go then renders its own directive, so `Refusal.relay` adds none and keeps Go's
verdict lines at both ends (today `event_bridge.py:232` prefixes
`Event write failed: ` to the first line, which would break the bracket).

### Order

Filed as E-2159 (Go) and E-2160 (Python), both implementing E-2155.

The Go helper and its check first: it owns the golden rendering and the
environment contract that Python consumes, and every refusal E-1063 ports into Go
is then born classified. Python second. The inventory already classifies every
site, so each side converts in one land, with no allowlist ratchet — a ratchet
would be unclassified sites passing the build, which is the default the plan
rules out.

## What did not fit the rule

1. **Faults and environment dominate REPORT (165 of 200).** They are REPORT
   because the agent cannot continue, but they name no decision. The `fault`
   factory gives them one standard directive instead of 110 invented decisions.
2. **Warnings the user should act on, but that block nothing, become inert.**
   The rule makes them NO-REPORT, and they are addressed to the human.
   *Settled 2026-09-18: these go to the errors channel — `faults.Record` on the
   Go side, a new `endless-go errors record` verb for Python — so the user sees
   them on the session-status badge and the agent spends nothing.* The set:
   output style installed but not active (`outputstyle.go:143`, fires on every
   `register`), invalid `worktree_ttl` re-warned on every claim and land
   (`event.go:721`), worktree dir not git-ignored (`bind.go:162`, and Python
   discards that stderr on exit 0), the unminimized-report notice
   (`report_cmd.py:149`), unsupported harness (`cli.py:948`), the sigil synonym
   question only the user can answer (`sigils.go:199`).
3. **REPORT messages hand the agent the bypass.** `--force` on every drop
   refusal, `git worktree remove` (`worktree_cmd.py:3614`), `reset --hard main`
   (`claude.go:1425`), `task update --status revisit` on a declined task
   (`session_cmd.py:429`, `:2647`), `--cascade` (`task_cmd.py:3214`),
   `--no-session` (`event_bridge.py:171`), `--write` for raw SQL (`cli.py:1236`).
   By the rule's letter a named command reads NO-REPORT; each is a user act.
   *Settled 2026-09-18: a REPORT renders the decision and not the bypass
   (`human_remedy` is human-only), and the commands that are the user's alone
   refuse an agent outright (E-2162) rather than offering it `--force`.*
4. **Remedies that do not exist in a foreign project.** `just install`
   (`session_states.py:92`, `statuses.py:100`, `cli.py:1080`,
   `worktree_cmd.py:3563`, the task-status/session-state relays), `just land`
   (`worktree_cmd.py:2895`), the retired `endless-sandbox` binary (`bind.go:53`,
   `sandbox.go:95`, `:97`), `docs/status-lifecycle.mmd`
   (`status_transition.go:57`), Endless's own task ids in the rebuild guard
   (`rebuild_guard.go:190`), `endless pivot`, which does not exist
   (`claude.go:2057`).
5. **Gates whose remedy the agent cannot perform.** The cwd gate says `/cd`,
   which the model cannot run, so every mid-session claim ends at the user
   (`claude.go:2161`). The revisit gate says ask via AskUserQuestion, a tool the
   same gate blocks (`claude.go:1201`). The `ended`-state gate is REPORT with
   nothing to decide (`claude.go:997+1055`).
6. **Classification is only as good as the error that reaches the site.** Faults
   are swallowed upstream and surface as not-found (NO-REPORT): `_live_sessions`
   returns `[]` on any endless-go failure (`session_cmd.py:1741`); a worktree
   in-use probe's cause is dropped because Python reads stdout
   (`worktree.go:120`); a failed Go schema change shows only "exit status 1"
   (`schemachange/errors.go:21`). Some real failures reach nobody at all: spawn
   launch failures after spawn-window reported success (`spawn_launch.go:31`,
   `:46`, `:54`), async hook faults, a dropped `$FULL` sigil (`sigils.go:285`).
7. **Refusals after side effects.** Several refusals fire after a partial write:
   `task update` plan/outcome files already committed (`task_cmd.py:5593`,
   `2642`, `2654`, `2694`), a spawn with the task already flipped to underway
   (`6744`, `7019`), a decision file written before a git failure raises
   (`worktree_cmd.py:2382`). The largest is in the event pipeline — see *Defects
   found* below.
8. **One message, two audiences.** `hook.go:55` (the halt notice goes to the
   agent on PreToolUse and to the human on UserPromptSubmit/SessionStart/Stop).
   `claude.go:2161` is written to the agent but only the user can act on it.
9. **Non-zero exits for non-failures.** "Nothing to roll back" and "already the
   shipped default" exit 1 (`minimizer_cmd.py:287`, `:296`); an unbounded
   "re-run" with no escalation while the Stop gate holds the turn
   (`report_cmd.py:191`, `:195`).

## Detection gap in trigger 1

The plan says trigger 1 keys on `CLAUDECODE`, `CLAUDE_CODE_SESSION_ID` and
`AI_AGENT`. It does not: `agent_env.present()` is `detect() != UNKNOWN`, and
`detect()` recognises only `CLAUDE_CODE_ENTRYPOINT=cli`, `claude-desktop`, or the
Desktop bundle id. An agent in the VS Code extension (`vscode`) or the SDK
(`sdk-py`) reads as a human and would never see a directive. Settled
2026-09-18: detection is NOT widened — that is the behaviour, not a gap to fix
here.

## Message defects to fix while converting

Every site is touched by the conversion anyway, so these ride along:

- `session_cmd.py:429`, `:2647`: `--status revisit` is not a legal edge from
  declined or obsolete (only → untriaged).
- `task_cmd.py:5298`: `--status <status>` is left unfilled.
- `task_cmd.py:3468`, `:3552`: `--status completed` is wrong for research and
  brainstorm (they finish through unreviewed) and for epics (derived).
- `task_cmd.py:4505`: says "have it release the task"; `task release` is disabled.
- `task_cmd.py:3122`: says task ids are reused; they have not been since E-1929.
- `db.py:524`: names `~/.endless/backups/`; backups go to `<config dir>/backups`.
- `claude.go:1498`: names `git commit --no-verify` as the bypass; the same regex
  blocks it.
- `claude.go:852`: claims a plan sync that no longer happens.
- `parent_cycle.go:55`: the command omits the `endless` prefix.
- `template.go:92`: prints its flag error twice (`flag.ContinueOnError` already
  wrote it, and `Run` prints the returned error again).

## Defects found

Surfaced while reading refusal sites. Every item was re-verified against the
code; the ones marked *reproduced* were run in a sandbox.

**Fixed on this branch** (cheaper than a task row):

- `session use/cd/show E-NNN` could never match: `_match_companions` read
  `active_task_id`, the key E-1969 renamed to `task_id`. Tests staged the old key
  and hid it.
- `verb list --json`, `phrase list --json` and `worktree list/current/show/for-task
  --json` crashed with `NameError`: `provenance` was called but never imported.

**Folded into E-2159** (same sites, same conversion): items 3-8 below, plus:

- `hookcmd`'s `init()` redirects the standard logger to stderr and `hook.log`,
  prefixed `endless-go hook:`, in every endless-go subcommand, before `--db` is
  parsed (E-2159, logging step).
- Faults that arrive at a refusal site as the wrong error:
  `_live_sessions` returns `[]` on any endless-go failure, so a broken install
  reads "No Claude session matches" (`session_cmd.py:1735`); `worktree drop`
  quotes the in-use probe's reason and drops its cause (`worktree_cmd.py:3534`
  prefers stdout); a failed `.go` schema change shows only "exit status 1"
  (`schemachange.go:232`, `runner.go:130`).

**Dispositioned 2026-09-18** (Mike: file, merge or fold). Items 3-8 are folded
into E-2159, because each sits on a site that task converts and each makes a
class or a verdict dishonest until it is fixed:

1. **Folded into E-1935** (ledger-rebuild trust): refused events are written to
   the durable ledger. *Reproduced.* `event
   emit` appends the event and git-commits the ledger segment
   (`eventcmd/event.go:300-310`) before `events.Execute` runs the guards
   (`executor.go:778`). A refused status transition rolls back the database and
   stays in the ledger; the projector's `replayTaskFieldsUpdated` applies
   `status` with no transition check. In the sandbox, two refused
   `--status confirmed` calls on an unplanned task each added a
   `task.fields_updated` line. Affects every guard in the executor (transitions,
   actor standing, parent cycles, phase, decision status), and every refusal an
   agent has ever hit through them is in the main ledger.
2. **Folded into E-1063** (the port embeds what Python reads from the source
   tree): `endless guide` fails on any non-editable install. `cli.py:982` and
   `guide_map.py:31` resolve `docs/guide` from the source tree; the wheel ships
   only `src/endless`. A wheel built from this tree contains no `docs/`. The
   first command CLAUDE.md tells an agent to run would fail for anyone who
   installs with pip, pipx, or `uv tool install` without `-e`.
3. **`decision add` with a repeated `--decides` id creates the decision, then
   fails.** *Reproduced.* Re-running the corrected command creates a duplicate
   (`decision_cmd.py:605` emits before the link loop at `:633`).
4. **`decision supersede` says "already superseded" while the decision is still
   `accepted`.** *Reproduced.* Any "already linked" error is rendered that way
   without checking status (`decision_cmd.py:1032`).
5. **`task add` crashes with a traceback when committing a new verb fails.**
   `main_commit.commit_path` raises `RuntimeError`; `task_cmd.py:409` catches
   only `ValueError`. No task is created and `verbs.jsonl` is left modified.
6. **Research justification dead end.** *Reproduced.* `task update --type
   research` on an already-justified research task refuses without
   `--justification` and refuses with it (`task_cmd.py:5612`, `:2640`, `:2694`);
   no command edits notes.
7. **The revisit gate blocks the AskUserQuestion it tells the agent to use.**
   Only Bash `endless task continue` is exempt (`claude.go:1133`).
8. **The commit-on-main guard is bypassed by a reworded command** — `git -C .
   commit`, `cd x && git commit` — because its regex is anchored at the start
   (`claude.go:1331`), while the bypass its message names is blocked.
9. **Dropped** (self_dev only, low reach): the sandbox reap guard hard-codes `main`
   (`reapguard.go:267`; `monitor.DefaultBranch()` exists), so `prune` always
   refuses on another default branch; `seed_worktree.go:46` re-derives the main
   database path instead of calling `mainConfigDir`.
10. **Already owned by E-1964**: worktrees whose sandbox predated E-1964 were
    left without the self-ignoring `.gitignore`, so they report the sandbox to
    git. Its migrate command had a bug; E-1964 is fixing it. This worktree was
    repaired in place by running E-1964's own provisioning function.

## Decisions (settled 2026-09-18 — do not re-open)

1. **`--format agent` fires the directive**, like `--agent`. Harness detection is
   not widened: an agent under an unrecognised harness reads as a human.
2. **Conditions the command cannot resolve render as `report_if`** — both
   branches and the consequence, for the agent to judge. Conditions it *can*
   resolve (actor, call path, process ancestry, state) are resolved in code.
3. **An unclassified error renders as a fault** — a Go error reaching a generic
   print site without a class, and an uncaught Python traceback.
4. **Warnings that ask the user to act but block nothing go to the errors
   channel**, not the agent's reply.
5. **User-only commands refuse an agent outright** rather than carrying a
   directive: `worktree drop`, `event rebuild-db --confirm`, `db restore`,
   `project unregister --purge`, the `setup` commands that write shell rc files
   and global Claude settings, and the user-owned status transitions. Filed as
   E-2162; this inventory classifies their messages REPORT.

## Where the work went

- **E-2159** — the whole conversion, both languages, Go first. E-2160 was merged
  into it (ED-1550: one cause is one task; a language boundary is a sequence
  inside a plan, not a second row).
- **E-2162** — the user-only refusal guard (decision 5).
- **E-2161** — retain a removed task's parent link instead of nulling it.
- **E-1935** — carries the refused-events-in-the-ledger finding as evidence.
- **E-1063** — carries the embed-the-guide finding, since the port is where it
  goes away.
