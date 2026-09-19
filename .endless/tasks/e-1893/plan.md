# Plan — E-1893: require replies to restate the task IDs they are about

## Decisions (user, 2026-08-08)

Delivery surfaces: **handoff templates, the E-1919 output style, and `endless guide`**.
Explicitly NOT the E-1803 PostToolUse nudge — E-1911 just rewrote that nudge and parked
E-1901's enforcement arm, so it is recently-settled ground this task will not disturb.

The style's reach gap is closed two ways: activate it for endless now, and add a
**dim FYI on stderr** telling a *human* operator the style is installed but inactive.
Optional in principle; treated as required for any project using Endless for now.

## Part 1 — handoff templates (one sentence, one place)

The analysis's open question (a line in each of seven templates vs a shared fragment)
resolves itself: `internal/templatecmd/templates/handoff/_close.tmpl` already exists,
already holds the reporting instructions, and is already parsed into every handoff
template's set via `handoffPartialNames`. Add the rule to the `handoff_close` define.

Wording must cover the crux from the analysis — the subject is often NOT the session's
own task, so the tmux window title does not supply the missing ID:

> Name the explicit task ID(s) in the first sentence that makes a claim about a task —
> including tasks other than your own, where my window title tells me nothing.

Do NOT add it to `handoff_verify` / `handoff_nonverify`; those are about the verify
command, and duplicating the rule invites drift between copies.

## Part 2 — E-1919 output style

The style's `## Lead with the IDs` section already says to open with the bare
identifiers the answer concerns. It does not stress the other-tasks case, which the
analysis calls the crux. Strengthen that section only — do not restructure it.

Because the style file is embedded, the change is made in
`internal/outputstylecmd/templates/Endless.md.tmpl`, then re-materialized into
`.claude/output-styles/Endless.md` with `outputstyle install --force`. Both copies must
be committed; a stale committed copy would silently shadow the intent.

`tests/tasks/e-1919-verify.sh` section 8 asserts the style body's contract clauses. Add
an assertion for the other-tasks wording so this cannot regress.

## Part 2b — demote Veracity, and replace it with a checkable rule

Evidence from ES-1050 (2026-08-15). Three veracity failures in three turns, all
the same shape: a fact stated confidently from memory that a one-line command
would have settled.

1. Claimed E-1893's plan mirror "went to the main checkout's plan directory".
   `find` showed the file exists nowhere on disk. Fabricated a location instead
   of saying "I don't know".
2. Claimed `worktree drop` "can take the branch with it". `drop_worktree` runs
   `git worktree remove` and nothing else. The `branch -D` calls grepped were in
   `_delete_orphan_branch` (branch reuse at creation) and the Go reaper — code
   paths never read, only matched.
3. Quoted a rule as "verify before asserting". No such text exists; the style
   says "Veracity: verify at the moment of surfacing". Quotation marks assert a
   provenance that was approximated from memory.

**Why this indicts the section.** All three were committed by the author of the
Veracity section, in turns discussing that section. An instruction cannot fix
this class: the failure is not a choice made against a known rule, it is a false
memory presenting as knowledge. At the moment of writing there is no felt
uncertainty for the rule to interrupt. This is the lever E-1785's arc already
abandoned once — E-1901 added a Stop gate with teeth, E-1911 parked it — and
ED-1531 named the working alternative: computable facts are produced by the tool
"so the agent never types them". All three failures were facts a tool could have
produced (`ls`, reading the matched function, `grep` of the file).

**Change to make.** Keep the Veracity section but demote it from mechanism to
nudge — it must not be presented as the answer to fabrication — and add one rule
that is mechanically checkable rather than aspirational:

> When you state a file path, a code behavior, or a quotation, the command that
> produced it appears in the same turn. Grepping is not reading: a match tells
> you a string occurs, not what the code around it does. If you cannot show the
> command, you do not know the fact — say so instead.

Unlike "verify at the moment of surfacing", this leaves a trace: a reader (or a
detector over the corpus) can see whether the substantiating command is present.

**Scope note for any future style optimization (E-1975).** The sections that are
compositional choices — the bar, form, Lead with the IDs, the disclosure-framing
ban — are legitimately style-shaped and tunable. Veracity is a construction
problem wearing a prose instruction's clothes; an optimizer pointed at its
wording would find noise and promote it. Style optimization is explicitly parked
until E-1975 is proven on the minimizer, per user direction 2026-08-15.

## Part 3 — `endless guide`

Add the rule to the reporting guidance in the guide's orchestration/reporting section,
for sessions not started from a handoff. Keep it to the same one sentence used in
`_close.tmpl` — three surfaces carrying three paraphrases is how the rule drifts.

Re-run `just regenerate-guide` if the section map changes.

## Part 4 — activate the style for endless

Commit `"outputStyle": "Endless"` into the main checkout's tracked
`.claude/settings.json`. Verified safe: `just claude-settings-init` rebuilds each
worktree's settings from the committed copy's non-hook keys plus the working copy's,
replacing only `hooks` (justfile:381-385), so the key survives worktree regeneration
rather than being dropped.

Note this is a genuine behavior change for every endless session, which is why E-1919
made it opt-in. It is being opted into deliberately here, not by default.

## Part 5 — the dim FYI (human-only, stderr, appended)

**Trigger:** any `endless` command run by a human. Never when run by an agent.

**Detection already exists — twice.** `agent_help.is_claude_code_agent()` and
`task_cmd._running_under_agent()` are byte-identical (`CLAUDECODE == "1"`). Consolidate
onto `agent_help.is_claude_code_agent()` and delete the duplicate rather than adding a
third caller — same reasoning that folded `templatecmd` onto
`monitor.ProjectRootFromCwd` in E-1919.

**Behavior:**
- Fires only when `not is_claude_code_agent()`.
- Fires only when the project has the style file present but `outputStyle` unset (or set
  to something else) in `.claude/settings.json` — i.e. exactly the inert state
  `outputstyle install` warns about.
- Writes to **stderr**, dim, appended after the command's normal output, so it never
  corrupts stdout for pipes/`--json` consumers.
- One line. Not an error, not a non-zero exit.
- Suppressed when the project has no style file at all (nothing to activate) and when
  `.claude/settings.json` is unreadable.

**Wording:** dim, and it must name the activation route, since there is no
`/output-style` command:

> FYI: the Endless output style is installed but not active — `/config output-style=Endless`

**Throttle — mtime marker, not the DB.** An earlier draft of this plan said to reuse
`monitor.ShouldThrottle`. That is wrong: it is Go-side and DB-backed (queries the
`activity` table), so calling it from the Python CLI would mean a subprocess on every
`endless` invocation — unacceptable overhead for a one-line FYI. Python's
`task_cmd._bg_throttle_warn` is a *count* threshold, not a time throttle, so it is not
the shape either.

Use a marker file instead: `$XDG_CACHE_HOME/endless/outputstyle-fyi/<project>.stamp`.
Print only when the marker is absent or older than the interval, then touch it. No DB,
no subprocess, ~5 lines. Default interval 4 hours, overridable via
`.endless/config.json`. A missing/unwritable cache dir means print (fail open) —
suppressing the reminder silently is the worse failure.

Do NOT ship an unthrottled per-invocation nag; that is the noise that got E-1901's gate
parked.

**Placement:** a single hook in the Python CLI's result callback / group teardown so it
appends after any subcommand, rather than being pasted into individual commands.

## Verification

Fold into the existing suite per the one-command rule:
`esu && ./tests/tasks/e-1893-verify.sh`

Checks:
1. `handoff_close` renders the ID rule for every handoff type (todo, bugfix, research,
   epic, brainstorm, respawn) — asserts the shared partial actually reaches all of them.
2. The rule appears exactly once per rendered handoff, not once per template that
   includes a partial.
3. The style body carries the other-tasks wording.
4. The embedded template and the committed `.claude/output-styles/Endless.md` are
   byte-identical — catches a re-materialization that was forgotten.
5. `endless guide` output contains the rule.
6. The FYI fires with `CLAUDECODE` unset and a project in the inert state.
7. The FYI does NOT fire when `CLAUDECODE=1`.
8. The FYI does NOT fire when `outputStyle` is already `Endless`.
9. The FYI goes to stderr, not stdout: stdout of a `--json` command still parses.
10. The FYI does not change exit status.
11. Only one agent-detection helper remains in the Python source.

## Out of scope

- The E-1803 PostToolUse nudge (user decision).
- A `--global` output style install; E-1919's project-scoped decision stands. If the
  per-project activation proves too narrow across 50+ sessions, that is a separate
  change against E-1785, not this task.
- Any enforcement gate. This task delivers instructions on three surfaces plus one
  human-facing FYI; it deliberately adds no teeth.


