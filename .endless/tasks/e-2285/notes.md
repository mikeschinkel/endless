## Scope added by E-2283 (Mike's decision)

Whichever of E-2284 and E-2285 first adds the `endless-go` startup `initialize()` step also converts `internal/jobs` to the same pattern:

- `jobs.Register` only appends. It does not panic on an empty or duplicate name.
- Those checks move into `jobs.Initialize() error`, which is called in that startup step.
- A failure exits through the refusal path and stops every `endless-go` command.

Pattern, rationale and the settings/check split: E-2283's outcome.

## Scope added by E-1398 (Mike's decision)

E-1398 "Add a command to install the Claude hooks" is superseded by this task
for its remaining half.

Already shipped: `endless setup claude-hook` installs the hook entries in the
user-scope Claude settings file, and `_repair_missing_hook_events` /
`_repair_hook_async_flags` repair a drifted install idempotently on every run.
Never shipped: anything that RUNS that comparison unprompted. Verified on main
8c63aac5c — `setup_claude_hook` has exactly one caller, the
`setup claude-hook` CLI command.

Why it is worth a check: on this machine two of the eight installed events
(StopFailure, Notification) were absent until a manual run on 2026-10-11.
StopFailure is Stop's ALTERNATIVE (E-2145), so without it a turn that dies on an
API error leaves the session reading `working`, and both `project monitor` and
auto-spawn's concurrency cap treat `working` as a live claim. The symptom is
throttled spawning with no error anywhere.

Decided with Mike, 2026-10-11:

- The check runs in the `endless-go` startup `initialize()` step the section
  above already describes — NOT in a hook event handler. Mike's reason: a hook
  should carry only what cannot be done elsewhere, because hooks are fragile
  and their scope is hard to control. A startup step covers the hook
  invocations anyway without being hook-specific. Reading one small JSON file
  per invocation is noise beside a process start and a database open; it is not
  a cost worth designing around.
- The expected event set MOVES to Go, and Python reads it from `endless-go`
  rather than keeping its own copy. One list, declared beside the handlers.
  `setup_claude_hook` already refuses when `endless-go` is not on PATH, so
  depending on the binary to enumerate the events costs that command nothing it
  did not already require.
- doctor REPORTS the drift and offers `--fix`, which is user-only (ED-1617).
  Nothing repairs the user's global Claude config as a side effect of another
  command.
- Three conditions count as drift: an expected event with no endless-go entry;
  an entry whose `async` flag disagrees with the sync set; and an entry whose
  command names a path that does not exist. The third is new — `_has_endless_hook`
  tests only for the substring "endless-go", so an entry pointing at a deleted
  or moved binary passes every check today while that event silently does
  nothing in every project. Report it as its own condition: the remedy differs
  (`just install` or re-running setup, versus adding the event).
- Fix tier: confirm, not auto. The repair only appends and never reorders or
  removes a foreign hook, but it edits a file Endless does not own.

Answered 2026-10-12, no longer open here: the Claude hook dispatch has ten event
cases while `CLAUDE_HOOK_EVENTS` installs eight. `PreCompact` is a real event
whose handler parses the transcript before compaction and was never installed;
`ExitPlanMode` is not an event name at all, so its case was never reachable.
Mike's answers: install `PreCompact` (synchronously), delete the `ExitPlanMode`
case rather than relocate it, and do NOT install the rest for safety — each
event is paired with a handler or left out, because the common path before the
dispatch switch refreshes `last_activity` on every invocation. E-2304 carries
the fix; E-2305 decides the rest of the catalog, including whether the expected
set is a list of names or a structure with matchers, which is the part that
affects what this task builds.



## Scope added by E-2375 (Mike's decision, 2026-10-24)

A project-scope check: the project's .gitignore covers every entry in `GITIGNORE_ENTRIES` (src/endless/register.py). Mike: `.endless/worktrees/` belongs in ALL Endless projects; init should ensure it and doctor should find it missing.

Evidence: go-dt was registered without the block, so every task worktree showed `.endless/worktree.json` as untracked, and `endless task verify` refused to run until the entries were added by hand.

`scaffold_gitignore` already adds the block on `project init`, but nothing checks a project registered before that, or one whose block was removed. The fix tier is auto: it calls `scaffold_gitignore`.

Init side, same matching code: `scaffold_gitignore`'s docstring says entries present "in any form" are left as-is, but it compares exact stripped lines. An anchored `/.endless/worktrees/`, which is what go-dt's .gitignore now uses (Mike's style), counts as missing, so re-running init appends a duplicate unanchored block. The check and init should share one matcher that treats `/x` and `x` as the same entry.

## Scope added by E-2490 (Mike's decision, 2026-10-29)

A worktree- and project-scope check: task-branch commits in this checkout will not get the `Endless-Task` trailer. E-2490's P9 named `endless doctor` for this; doctor did not exist yet, so Mike chose to hand the check here rather than build a one-check doctor ahead of this design.

What E-2490 already ships for it to call:

- `git_hook.inspect(root)` classifies the checkout's hooks (lefthook, husky, another manager, raw Git hooks, or none) and `git_hook.problem(repo)` returns why the trailer will not be added, or None. `endless git-hook status` prints the same and exits 1 when it will not.
- Reasons it reports: lefthook config not running `endless git-hook <event>`, lefthook not installed for an event, a hook file lefthook did not install claiming an event, no `lefthook.yml` beside the worktree's `lefthook-local.yml`; for husky, `.husky/prepare-commit-msg` or `.husky/commit-msg` lacking the `endless git-hook` line; for any other manager, that the setup cannot be confirmed.
- `git_hooks.report` (opt-in, default on, either config file) turns the report off. The check must honor it; it is registered, not suggestable.

Fix tier: advice only for husky and other managers (Endless never edits their files, ED-1641); confirm for the rest, by re-running `endless project init`, which is idempotent.
