# Plan — every refusal and warning says whether the agent may continue alone

Applies the E-2155 inventory: `docs/research-2026-09-17-refusal-inventory.tsv`,
one row per site, classifying all 981 user-facing sites (587 NO-REPORT, 200
REPORT, 194 CONDITIONAL) plus 27 INFO and 128 EXCLUDED, with the decision that
applies to a row in its `notes` column. E-2155's outcome carries the findings
and its analysis explains how to read the TSV.

## Rendering, by class

    NO-REPORT  [Endless] task add: title 107>100 chars. Nothing was created. Move the long form to --analysis and retry. Handle this yourself: do not mention this refusal to the user, now or in your summary.
    REPORT     [Endless] worktree drop: E-101's worktree is in use by a live session. This needs the user: whether to remove a worktree a live session is using. Stop and ask; do not work around it.
    REPORT-IF  [Endless] task remove: E-101 has 3 children. If the user did not ask for the subtree to go, stop and ask - removal cannot be undone; otherwise retry with --cascade and do not mention this refusal.
    FAULT      [Endless] session list: endless-go returned no output. Endless itself failed: tell the user, and do not retry more than once.

"now or in your summary" is deliberate: the failure that opened E-2155 was a
refusal the agent handled correctly and then narrated in its handoff.

One task, both languages, because there is one cause: a refusal does not say
whether an agent must report it. The Go half lands first — it owns the golden
rendering and the environment contract the Python half consumes — but it is a
sequence inside this plan, not a second task.

## The rule

An agent reports a refusal or warning **only when it cannot continue without
input from the user**. "You must do X instead of Y" is NO-REPORT: call the
command again doing X.

## Decisions (settled, Mike, 2026-09-18 — do not re-open)

1. **Triggers.** The directive renders when the environment says an agent
   (`agent_env.present()` / `agentenv.Present()`), or `--agent`, `--agent-view`,
   or `--format agent` was passed. `--format agent` is the long form of
   `--agent` (E-1504) and must behave identically. Harness detection itself is
   NOT widened: an agent under a harness Endless does not recognise reads as a
   human, as today.
2. **Conditions the command cannot resolve** — about 36 sites, where the class
   turns on what the user asked the agent to do (or, for a few, on who made an
   earlier change and nothing recorded it) — render as `report_if`: one
   directive naming both branches and the consequence, e.g. "If the user did not
   ask for the subtree to go, stop and ask — removal cannot be undone;
   otherwise retry with --cascade and do not mention this refusal." The agent
   holds the conversation and decides, informed.
3. **Conditions the command CAN resolve** — about 16 sites — are resolved in the
   code, not handed to the agent. Four sources, all already available: the actor
   (who ran it), the call path (Endless computed the value vs a caller passed
   it), process ancestry (whether the processes holding a sandbox are the
   agent's own), and state already in hand (a `session resume --review` tree, an
   `ErrNoProject` vs a database error, EPIPE vs a failed write). Two worth
   naming: the live-session ambiguity refusals can resolve the session from
   `CLAUDE_CODE_SESSION_ID` instead of asking, and the interactive-prompt "file
   not found" refusals know the path came from whoever ran the command.
4. **An unclassified error renders as a fault.** A Go error reaching a generic
   print site without a class, and an uncaught Python traceback, render as
   "Endless itself failed — tell the user, do not retry more than once."
5. **Warnings the user should act on that block nothing go to the errors
   channel**, not the agent's reply: output style installed but inactive, an
   invalid `worktree_ttl`, a worktree directory not git-ignored, an unsupported
   harness, the unminimized-report notice, the sigil synonym question. The user
   sees them on the session-status badge and `endless errors show`; the agent
   spends nothing. Go calls `faults.Record`; Python needs a new
   `endless-go errors record` verb (`errorscmd` has show|clear|codes today), and
   each warning class takes a code in the `internal/faults` catalog.
6. **User-only commands are out of scope here** and belong to their own task
   (filed alongside this one): they refuse an agent outright rather than
   carrying a directive. This plan classifies their messages REPORT and does not
   build the guard.

## Build — Go first

1. **The package** (name the implementer's call; `internal/refusal` below), with
   class-named constructors only, so a site cannot construct a refusal without
   choosing a class: `NoReport(summary, remedy)`, `Report(summary, decision)`,
   `ReportIf(summary, condition, remedy, decision)`, `Fault(err)`, `Warn`,
   `Info`, `Passthrough()` for a child process's own stderr, `(*Error).Exit`,
   `Block(*Error)` for a PreToolUse refusal, and `From(err)` for a generic print
   site (decision 4).
2. **Audience**: `agentenv.Present() || os.Getenv("ENDLESS_AUDIENCE") == "agent"`.
   Hook output takes its audience from the hook event and exit path instead —
   PreToolUse exit 2, block reason and additionalContext reach the model;
   UserPromptSubmit, SessionStart exit 2 and Stop exit 1 reach the human; exit 0
   and async events reach nobody, so nothing user-facing may go there.
3. **Rendering**: humans get today's text byte for byte. An agent gets E-2097's
   bracket — one verdict line first and last — with the directive inside the
   verdict line, so a truncating pipe keeps it at either end. A REPORT names the
   decision and never the bypass: `human_remedy` renders for humans only, so
   `--force`, `git worktree remove` and `reset --hard main` stop being offered to
   an agent. The verdict states whether anything changed (E-2097), which is what
   makes step 8 part of this task and not a separate cleanup.
4. **A golden file** of rendered verdicts, one per class, asserted by a Go test
   and again by the Python test in step 10.
5. **The check**: a Go test (`go/parser`) over non-test files in `internal/` and
   `cmd/` failing on any `os.Stderr` reference outside the package — which
   covers `fmt.Fprint*(os.Stderr`, `cmd.Stderr = os.Stderr`,
   `log.SetOutput(os.Stderr` and `slog.NewTextHandler(os.Stderr`. There are 308
   today in 45 files; the 3 stderr writers passed as parameters become the
   package's own type, which has no `Write`, so an unclassified write through one
   does not compile. The failure message states the rule and lists the
   constructors, as `tests/test_no_self_dev_ids.py` states its rule when it fires.
6. **Logging**: move the standard-logger setup out of `hookcmd`'s `init()`,
   which today redirects `log` for every endless-go subcommand before `--db` is
   parsed, into the package, writing to the log file only.
7. **Convert every `lang=go` row** of the inventory TSV to its class.

## Build — Python second

8. **The helper**: extend `agent_help.agent_error` (E-2097) rather than adding a
   parallel one; that module has been consolidated twice. `Refusal`
   (a `click.ClickException`) with class-named factories only: `no_report`,
   `report`, `report_if`, `fault`, `relay` (endless-go output, whose class
   already travelled with it, so no second directive and Go's verdict lines stay
   at both ends), `relay_foreign` (git and hook scripts, classified at the site);
   plus `warn.no_report`, `warn.report`, `warn.record` (decision 5), `info`,
   `passthrough_exit`.
9. **`agent_facing()` honours `--agent` and `--format agent`**, detected in the
   root group's argv pre-scan that already consumes `--agent-view` — one place,
   not per call site. When it is true, set `ENDLESS_AUDIENCE=agent` so endless-go
   subprocesses render for the same audience.
10. **Two categories classified once, in the root group's `main`**: Click's own
    usage errors and `ParamType.fail()` are NO-REPORT usage by definition; an
    uncaught exception is a fault (decision 4).
11. **The check**: `tests/test_refusal_sites.py`, an AST walk of `src/endless/`
    failing on any `ClickException`/`UsageError`/`BadParameter`/`Refusal`
    construction or subclass, `click.echo(..., err=True)`,
    `print(..., file=sys.stderr)`, `sys.stderr.write(`, or a non-zero
    `sys.exit`/`SystemExit`/`ctx.exit` outside the helper module.
12. **Convert every `lang=py` row** of the inventory TSV to its class.

## Defects this fixes on the way through

Each sits on a site being converted, and each makes a classification wrong or a
verdict dishonest until it is fixed.

13. **Refusals that fire after a durable write**, which makes the verdict's
    "nothing changed" a lie: `decision add` with a repeated `--decides` id emits
    `decision.created` before validating the links, so the decision exists and a
    re-run duplicates it; `task add` leaves `verbs.jsonl` modified when the verb
    commit fails; `task update` refusals fire after the plan and outcome files
    are written and committed; `spawn` refusals fire after the task is already
    `underway` with a worktree. Move each check before the first durable write,
    or state plainly in the verdict what was already written.
14. **`task add` crashes with a traceback** when committing a new verb fails:
    `main_commit.commit_path` raises `RuntimeError` and the `task add` path
    catches only `ValueError` (decision 4 covers the rendering; the catch is the
    fix).
15. **`decision supersede` reports "already superseded"** for any "already
    linked" error, without checking the decision's status, so it says a decision
    is retired while it still governs.
16. **The research justification dead end**: `task update --type research` on a
    task that already carries a `## Justification` section refuses without
    `--justification` and refuses with it, and no command edits notes. A NO-REPORT
    class is dishonest while the remedy is impossible.
17. **The revisit gate blocks AskUserQuestion**, the tool its own message tells
    the agent to use; only Bash `endless task continue` is exempt. Same
    impossible-remedy problem.
18. **The commit-on-main guard**: its message names `git commit --no-verify` as
    the bypass, which its own regex blocks, while a reworded `git -C . commit`
    passes unchecked.
19. **Make the cause reach the site, or the class is wrong**: `_live_sessions`
    returns `[]` on any endless-go failure, so a broken install reads as the
    NO-REPORT "No Claude session matches"; `_worktree_in_use_probe` prefers the
    probe's stdout, so `worktree drop` quotes its own reason and drops the cause;
    a failed `.go` schema change reports "exit status 1" because the runner's
    reason goes to a stderr the caller discards.

## Message defects to fix while each message is open

`_resolve_resume` and `_apply_revisit_intent` tell the user to revive a declined
task with `--status revisit`, which is not a legal edge (only `untriaged` is);
`_reopen_task_core` leaves `<status>` unfilled; `_require_status_allowed_for_type`
and `_refuse_cascade_across_typed_descendants` name `--status completed` for
research and epics; `_check_task_ownership` names the disabled `task release`;
`_orphan_refusal` says task ids are reused; `_migrate_v5` names a backups
directory under the home directory rather than the config directory's;
`blockCommitOnMainIfApplicable` names a bypass its regex blocks;
`handlePostToolUse` claims a plan sync that no longer happens;
`ValidateNoParentCycle` omits the `endless` prefix; `templatecmd.Run` prints
`runRender`'s flag error twice; `bindCmd` and the sandbox "already exists" and
"stale sandbox directory" errors name the retired `endless-sandbox` binary;
`refuseRebuildDBConfirm` cites Endless's own task ids; `ValidateStatusTransition`
cites a file a foreign project does not have; and every `just install` /
`just land` remedy, which exists only in Endless's own checkout.

## Sequencing with E-1063

Land before E-1063's port begins, so every refusal ported into Go is born
classified and the check catches any the port misses. Recorded as `relates_to`,
not a blocking relation: Mike sequences E-1063 by hand.

## No ratchet

The TSV classifies every site, so each language converts in one pass. An
allowlist of unconverted sites would be exactly the unclassified default this
task exists to end.

## Verification

- `just test` and `just test-go` pass, including both new checks.
- Adding `fmt.Fprintln(os.Stderr, "x")` to any Go package, or
  `raise click.ClickException("x")` anywhere in `src/endless/`, fails the build
  with a message naming the rule.
- As an agent (`CLAUDE_CODE_ENTRYPOINT=cli`), `endless task add` with a 107-char
  title prints the bracketed verdict carrying the NO-REPORT directive; as a
  human, stderr is byte-identical to today's.
- `--format agent` and `--agent` produce the same rendering; `ENDLESS_AUDIENCE=agent`
  alone produces it for endless-go.
- An illegal status change relayed from endless-go prints Go's verdict
  unprefixed at both ends.
- `endless errors show` lists a recorded warning after running `endless register`
  on a project whose output style is installed but inactive.

## What the build changed about this plan (recorded at implementation)

Written after the work landed in two passes: a first implementation, and a
re-implementation when the first would not rebase. Both are below, because the
second pass is where most of the deviations came from.

### The rebase, and why the first pass was redone

The first implementation sat unlanded for ten days while main moved. `just land`
then failed on a conflict `endless worktree diagnose` classified as **symbol
supersession (proven)**: the branch classified refusals in code main had since
DELETED — the whole triage-context/triage-claim surface, plus `phrase_cmd.py`
and `triage.py`. Resolving in place would have reintroduced calls to functions
that no longer exist, which is exactly the failure `diagnose` refuses to
prescribe a recovery for.

So the WORK was re-based, not the diff: the old head was tagged, the branch
reset to main, the 82 files main had not touched restored verbatim, and main's
version taken for the 51 it had — then re-converted. Main had restructured most
of the large ones, so a hunk-by-hunk merge would have been the riskier path. One
merge that was attempted proves it: preferring this branch's side in
`status_transition.go` reintroduced the early return E-2197 had just removed,
and only main's own test caught it.

Three pieces of the first pass were superseded outright and are gone:

1. **The commit-on-main guard.** E-2177 rewrote it, and far better — it walks
   the shell command, so `cd <dir> && git commit` and `git -C <dir> commit` are
   both judged in the directory the commit RUNS in, and heredoc bodies are
   stripped. This task's narrower regex fix and its test were dropped. What
   survives is the wording E-2015 filed: a BLOCKED action no longer calls itself
   "highly discouraged", and neither main guard teaches a hand-made
   `git worktree add`, which skips the provisioning hook.
2. **E-2177 also closed** the heredoc false positive this task tripped over
   twice while documenting the guards.
3. **The foreign-hook-build warning**, moved to the errors channel in the first
   pass, has no site at all now: E-2166 removed the worktree hook pin, so
   `warnForeignHookBuild` and `monitor.ForeignHookBuild` are gone from main. Its
   catalog code went with it rather than shipping a code with no producer.

### Deviations from the plan as written

1. **The Go check also forbids `flag.NewFlagSet`.** The plan scoped it to
   `os.Stderr`, which a plain `flag.FlagSet` never names: it writes its error
   and usage block to stderr from INSIDE the flag package, and
   `flag.ExitOnError` prints and exits before a site can classify anything. The
   check would have been decorative for every flag error. `refusal.NewFlags`
   captures that text, and `Flags.ExitOnHelp` restores the exit 0 that
   `ExitOnError` gave `-h` — without it, asking for help reports failure.

2. **`testdata/` is out of scope for the check.** A fixture program under it
   (`internal/liveview/testdata/reexecprobe`) is built by the test that needs it
   and by nothing else, so a message it writes reaches a harness, not a user.
   `go build ./...` skips these for the same reason.

3. **`internal/refusal` grew five things the plan did not name**: `NewFlags` and
   `ExitOnHelp`, `InitLog`/`SlogHandler` for step 6, `Faultf`/`Infof`, a
   nil-safe `Error()` (several gates spell "nothing to refuse" as a nil
   `*Error`), and copy-on-write setters — a refusal is often a package-level
   value reached through `From()`, and one caller naming its command would
   otherwise write that command onto a value every other caller shares.

4. **The verdict strips what it has already said** — both the `endless-go <verb>: `
   binary prefix and the command's own name. Both belong in the message a person
   reads; printed adjacent to the verdict's own opening they are the same words
   twice, in the one line whose value is density. Stripped in one place rather
   than at ~450 sites.

5. **`agent_help.run_standalone`.** Plan step 10 puts the two unclassified
   categories in the root group's `main`. Click's standalone mode prints and
   exits itself, so it is switched off and its block replicated — and that block
   IS refusal machinery, so it lives beside `Refusal.show` rather than in `cli`.

6. **Python reads `ENDLESS_AUDIENCE` too**, snapshotted at import so this
   process's own export cannot feed back as its input. The plan had Python
   writing it and Go reading it; one-way meant an operator who exported it got
   an agent rendering from Go and a human one from Python, from one command.

7. **Plan item 5 covers five warnings, not six.** The sixth — a worktree
   directory not git-ignored — went with `internal/sandboxcmd/bind.go` (E-1964)
   and has no site. The five are WARN-0021 through WARN-0025
   (`output-style-inactive`, `worktree-ttl-unreadable`, `unsupported-harness`,
   `report-unminimized`, `sigil-synonym`), each with a `docs/errors.md` section
   whose "What to do." paragraph is held byte-identical to its catalog remedy.
   Numbering starts at 0021 because main spent 0001-0020 meanwhile.
   Two notes on how they landed:
   - **The unsupported-harness directive stays on stderr.** It tells the AGENT
     to stop invoking Endless, and an agent that keeps going is the failure it
     exists to prevent. What moved to the channel is the half the agent cannot
     act on, for the user who chooses which harness they run in.
   - **The sigil synonym question is no longer injected at all.** It used to
     cost a turn's attention on every prompt using the new token, for a question
     only the user can answer about their own vocabulary.
   - `endless-go errors record` already existed; the plan expected it to need
     building.

8. **`cmd/endless-migrate`'s dependency allowlist admits `refusal` and
   `agentenv`.** Its test says the fix is never to widen the list — but that
   rule governs packages that expect a schema, and these two import only
   stdlib between them. Main's own E-2192 widened the same list on the same
   reading. The property the test defends is intact, and it is transitive, so
   the pair cannot smuggle a third package in later.

9. **Conditions resolved in code rather than handed over.** Of 80 CONDITIONAL
   `lang=go` rows, 25 became `ReportIf`; the rest were settled from the actor, a
   sentinel, an `errors.Is`, or state already in hand, per decision 3.

10. **Byte-identity needed its own audit, twice.** The rule is that a person's
    stderr does not change, and the first audit hid its own worst case by
    stripping the appended "Nothing was …" clause before comparing — which is
    precisely the drift it existed to catch. Re-run honestly it found seven
    sites, all since pinned with `text=`. A related one-place fix: the
    human-only remedy now joins with a space only when it continues the
    sentence, so a bypass that is its own paragraph no longer leaves a trailing
    space on the line above it.
