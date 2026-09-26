# Plan: make the Claude hook differentiate a command that RUNS from one that is only MENTIONED

Decisions by Mike, 2026-09-26, answering the open questions put to him in chat.
The threat model stands as recorded in --analysis: gates catch INNOCENT
INCORRECT USAGE, not errant behaviour. Nothing here is a security boundary.

## Scope, and what is NOT in it

In scope, one cause: the hook reads the raw Bash command string and cannot tell
running a command from mentioning it (in quotes, a commit message, a heredoc).

1. The hook's text-inferred STATE WRITES (claim, confirm) — deleted.
2. The claim handoff message those same matches triggered — moved to the command.
3. The text-matched GATES — taught to ignore quoted and heredoc mentions.
4. The commit-on-main gate — judge the directory the commit actually runs in.
5. A comment parking the `task report` detector.

Not in scope, owned elsewhere:
- **E-2179** owns every `task chat` line (hook detection, `StartChatSession`,
  the declaration gate's suggestion). If it has landed, the chat block in
  `handlePostToolUseSession` is already gone.
- **E-2180** owns `endless phrase` and the Python pattern-matcher half of
  `matchers.py`. This task deletes the Go `internal/matchers` package instead,
  because removing the hook's lookups is what orphans it.
- **E-1983's unbound-worktree gate stays exactly as it is** (write tools only,
  no escape regex).
- The `task report` detector's matching is NOT fixed here — see item 5.

## 1. Delete the hook's text-inferred state writes

In `internal/hookcmd/claude.go`, `handlePostToolUseSession`:
- Delete the `claim` detection and its `monitor.StartWorkSession` call.
- Delete the `confirm` detection and its `monitor.CompleteTask` call.

Why deleting is correct and not merely safe, verified in the code:
- `endless task claim` already does the whole job: it emits
  `task.status_changed` → `underway` and `task.claimed`, and `execTaskClaimed`
  binds the session. The hook's `StartWorkSession` predates that executor
  (E-1242) and has been a duplicate writer ever since — a no-op in the happy
  path, and a wrong write whenever the text names a different id or carries
  `--unattended`.
- For people, nothing changes: hooks fire only on a Claude session's tool
  calls, so a claim typed in a shell was always bound by the CLI alone.
- `CompleteTask` set `status='confirmed'` from ANY status, skipping the CLI's
  outcome, cascade and lifecycle checks — on a verb agents are told never to run.

With chat gone (E-2179) nothing is left in `handlePostToolUseSession` except
the handoff emission, which item 2 moves. So:
- Delete `handlePostToolUseSession` and its call site.
- Delete `actionStart`, `actionConfirm`, `scopeTask` and the `matchers` import.
- Delete `monitor.CompleteTask` if it has no other caller. Keep
  `StartWorkSession` only if something else calls it; otherwise delete it too.
- Delete the Go `internal/matchers` package and its test — orphaned.

**Test to confirm, as Mike asked — the bind must still happen without the hook.**
End to end in a sandbox: run `endless task claim E-<n>` with
`CLAUDECODE=1` and `CLAUDE_CODE_SESSION_ID` set (the in-process rung of the
session ladder), and assert (a) the session row is bound to the task, (b) the
task is `underway`, (c) the session is `working` or becomes so on its next hook
event. (c) is the one thing the hook's write did that the executor does not: it
set `state=working`. Every hook event already wakes the session; the test proves
it rather than assuming it.

And the regressions, each driven through the hook with a PostToolUse payload:
- a heredoc whose body names a claim of a real, unheld task → session stays
  unbound, task status unchanged;
- `endless task claim E-<n> --unattended` → the hook does not bind;
- a commit message naming a confirm of a real task → status unchanged.

Build every trigger string in these tests from parts at runtime. A test file,
or the Bash command that writes it, containing a literal claim or confirm of a
numeric id fires the very bug being fixed — which is exactly how this bug was
found (four spurious handoffs in one session).

## 2. `endless task claim` prints the claim handoff itself

Today one text match did two things: the bind (item 1) and E-1822's handoff —
"You just claimed E-<n> into an already-running session… treat this as the
instructions for this task". With the match gone, nothing would trigger the
handoff. Keeping the match for the message alone would keep firing on mentions
and tell the agent to switch to whatever task the text named.

So the command that performed the claim reports it:
- Add an `endless-go` subcommand that renders the claim handoff for a task —
  reuse `claimHandoffContext` / `claimHandoffVars` from
  `internal/hookcmd/claim_handoff.go`, which already assemble the var map and
  render the `handoff/claim` template. Keeping the renderer in Go means E-1063's
  port inherits it instead of re-deriving it; the Python side is only a caller.
  Keep its existing harness check (`TestClaimHandoff_ChecksTheHarness`).
- In `claim_item` (`src/endless/task_cmd.py`), after a claim that bound a
  session, print that rendered handoff — only when `agent_env.present()` says an
  agent is running this process. Use that helper, not a hand-rolled
  `CLAUDECODE` check; it is the project's one answer to "agent or person?".
- Not printed for `--unattended`, and not on spawn's pre-claim path (spawn
  delivers the handoff as the launch prompt).
- Delete the hook-side emission. Update `claim_handoff_test.go`: it currently
  seeds a project `matchers` config to trigger the hook path, which no longer
  exists.

Test: an agent-present claim prints the handoff; a person's claim does not; an
`--unattended` claim does not.

## 3. Text-matched gates ignore quoted and heredoc mentions

`cmdPos` already exists in `claude.go`: it anchors a match to command position
(start of the command, or after `;` `&` `|` or a newline) and refuses to cross a
quote. That is what lets the worktree-removal gate ignore `echo '… worktree
drop'`, and it admits wrapper prefixes (`uv run endless …`, a path-qualified
binary) and a leading `cd x &&` for free.

Two gaps close here:

**a. A heredoc-stripping helper.** Before matching, remove heredoc bodies from
the command string. `cmdPos` treats a newline as a command boundary, so today a
heredoc line that BEGINS with a command looks like the command — verified for
both the worktree-removal gate and the landed-suite gate. Recognize
`<<DELIM`, `<<'DELIM'`, `<<"DELIM"` and `<<-DELIM` (leading tabs allowed on the
closing line), strip through the closing delimiter line, and handle more than
one heredoc per command. Leave `<<<` here-strings alone. Require the delimiter to
start with a letter or underscore so arithmetic like `$((1<<2))` is not mistaken
for one. Small: roughly thirty lines plus table tests.
Known limit, accepted under the threat model: an UNQUOTED heredoc expands
`$(…)`, so a command substitution inside one really does run and would be
stripped unseen. Nobody writes that by accident.

**b. Apply it, and `cmdPos`, to every text-matched gate:**
- **sqlite on `.endless/`** (`sqliteEndlessRe`). Today it is unanchored and
  refuses plain mentions — a commit message saying never to point the sqlite3
  CLI at a `.endless/` database is refused (verified). It also refused the
  command that WROTE THIS PLAN: the heredoc carrying this very example tripped
  it, and the file had to be written without Bash. Anchor it at `cmdPos`; a
  leading `cd <path> &&` keeps working because `&&` is a boundary.
- **pause-on-revisit escape** (`revisitClearVerbRe`). Today unanchored, so a
  quoted `endless task continue` releases the gate for one call. Anchor it at
  `cmdPos`. The one new miss is `bash -c "endless task continue"`: the gate
  blocks it and the agent runs the command plainly — the safe direction.
- **worktree removal** (`worktreeRemovalRes`) and **landed-suite run**
  (`suiteRunRe`) already use command-position anchoring; they gain only the
  heredoc stripping.

Tests, per gate: a real invocation (bare, after `cd x &&`, with a wrapper
prefix) is still refused or, for the escape, still admitted; a quoted mention, a
commit-message mention and a heredoc line are not. Plus table tests for the
stripping helper itself, including `<<<` and `$((1<<2))` left intact. As in
item 1, assemble trigger strings at runtime — these gates fire on the Bash
command that writes their own tests.

## 4. The commit-on-main gate judges the directory the commit runs in

Today `gitCommitRe` (`^\s*git\s+commit`) matches only when the whole command
starts with `git commit`, so `cd <path> && git commit` — the most common form an
agent uses — is never examined.

**Do NOT just widen the regex.** The gate decides "is this main?" from the
SESSION's cwd, not from where the commit runs. Widened naively it would REFUSE a
legitimate `cd <worktree> && git commit` from a session sitting in main, and
still MISS `cd <main> && git commit` from a session sitting in a worktree.

So:
- Match `git commit` at `cmdPos`.
- Work out the directory the commit runs in: the last `cd <path>` in the same
  `&&`/`;` chain before it, or `git -C <path> commit`; resolve a relative path
  against `payload.CWD` and expand `~`; fall back to `payload.CWD`.
- Run `isInMainCheckout` and `isInActiveMerge` against THAT directory.

Tests: from a worktree session, `cd <main> && git commit` is refused and
`git -C <main> commit` is refused; from a main session,
`cd <worktree> && git commit` is allowed; a bare `git commit` behaves as today.

Known limit: a `cd` made in an EARLIER Bash call is invisible to this call's
command text. Whether `payload.CWD` tracks the shell's persisted directory was
not verified; note what you find.

## 5. Park the `task report` detector with a comment

Above `taskReportRe` and `reportRelayInstruction`, state that the report
channel is switched off (`minimizer.enabled: false` in project config) pending
E-2042 ("Make the reply minimizer trustworthy enough to leave enabled"), that
its implementation was found unworkable and will be revisited before it is
enabled again, and that its text matching should not be designed around or
"fixed" until then. Use the config key as it is spelled today; Mike wants
`minimizer` renamed `copyeditor`, which is not this task's change.

## 6. Keep the refusal inventory's anchors current

`docs/research-2026-09-17-refusal-inventory.tsv` anchors rows to file+symbol,
and `tests/test_refusal_inventory_anchors.py` fails if a symbol disappears.
Every symbol this task renames or deletes needs its row updated or marked
RETIRED — `CompleteTask`/`StartWorkSession` rows if any, the gate symbols if
renamed.

**Do not treat the inventory as accurate or complete.** It was written
2026-09-17 and the code has drifted since. The anchor test checks only that each
row's file and symbol still exist — not that the message text still matches, and
not that every refusal has a row. So expect gaps: a gate this task touches may
have no row, or a row whose message is stale. Fix the rows this task's changes
affect; do not audit the rest of the inventory here.

## Docs

Any guide text that describes the claim handoff as injected by the hook, or
the hook as recording claims or confirms from Bash commands. Run
`just guide-check`.

## Verification

- The end-to-end claim test in item 1 passes: the bind happens with no hook
  involvement.
- The three item-1 regressions pass: no text-driven bind, no `--unattended`
  override, no text-driven confirm.
- `grep -rn "StartWorkSession(payload\|CompleteTask(payload\|ActionRegex\|internal/matchers" internal/ cmd/`
  returns nothing.
- Each gate's mention-vs-invocation table passes (item 3), and the
  commit-directory table passes (item 4).
- `just build`, `just test`, `just test-go`, `just guide-check` green, and the
  task's verify suite folds these in as its first, fail-fast check.
