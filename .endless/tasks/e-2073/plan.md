# Make the worktree-removal prohibition categorical in the handoff templates

## Context

Every spawned session's handoff carries this rule:

    7. Don't run `endless worktree land`/`drop` without asking.

That form asks the session to evaluate a precondition — *have I been asked?* —
and that evaluation is where it fails. On 2026-08-25 two separate sessions
destroyed worktrees within ten minutes of each other. Neither overrode the rule.
Both concluded the precondition was satisfied by conversational text that did not
satisfy it (in one case, "if it can be reaped, you don't need to tell me about
it" — a remark about what is worth reporting, read as authorization to act). One
of the two caused real damage.

`land` and `drop` do not deserve the same rule. Landing is the normal end of a
task and the spawning session owns the timing, so "ask first" is right. Removal
is destructive, is never part of finishing a task, and has no benign case a
spawned session needs. It should carry no precondition at all.

## The change

Six templates carry the line. Five share the numbered form:

    internal/templatecmd/templates/handoff/todo.md.tmpl        line 16
    internal/templatecmd/templates/handoff/bugfix.md.tmpl      line 18
    internal/templatecmd/templates/handoff/research.md.tmpl    line 16
    internal/templatecmd/templates/handoff/brainstorm.md.tmpl  line 18
    internal/templatecmd/templates/handoff/epic.md.tmpl        line 27

Replace, in each:

    7. Don't run `endless worktree land`/`drop` without asking.

with:

    7. Don't run `endless worktree land` without asking. NEVER remove a
       worktree — not `endless worktree drop`, not `endless worktree reap`,
       not `git worktree remove`. The spawning session owns removal. If
       removal looks warranted, say so once and stop.

The sixth is inline mid-sentence and needs its own edit rather than a blind
substitution:

    internal/templatecmd/templates/handoff/claim.md.tmpl       line 19

    ... STOP and ask. Don't guess. Don't run `endless worktree land`/`drop`
    without asking.

becomes:

    ... STOP and ask. Don't guess. Don't run `endless worktree land` without
    asking, and NEVER remove a worktree — not `endless worktree drop`, not
    `endless worktree reap`, not `git worktree remove`.

Naming three commands rather than one is deliberate: a `drop`-only prohibition
is satisfiable by reaching for `git worktree remove`, which is the same outcome
by another route. The rule has to name the outcome.

Check `_mechanics.tmpl` and `_close.tmpl` for a copy of the same sentence before
finishing — the five numbered files were found by grep for "worktree land", and a
paraphrase would not have matched.

## Scope of the edit

Edit the embedded templates only. Do not write to `.endless/templates/`.

## Test

No test asserts the current wording, which is why it could have been reworded or
dropped silently. Add one so the rule cannot regress. `internal/templatecmd/`
already has the right shape in claim_handoff_test.go (TestRender_Claim_* render
a handoff and assert on its content). Add a test that renders each task type's
handoff and asserts the rendered text contains "NEVER remove a worktree" and does
NOT contain "land`/`drop` without asking".

Assert against the RENDERED output, not the file bytes, so the test also covers
the claim path and any future template that grows a copy of the rule.

## Verification

Fold into `tests/tasks/e-NNNN-verify.sh` as a fail-fast unit gate plus:

1. `go test ./internal/templatecmd/` passes, including the new test.
2. Rendering a handoff for each of the five task types shows the new rule and no
   occurrence of the old sentence.
3. `just test` and `just test-go` pass project-wide.

## Second half: the gate (added after the prose landed)

The original plan put a PreToolUse hook out of scope and left filing it to
Mike's judgement. That was wrong twice over, and he said so: the hook is not a
separate feature from a task whose entire subject is that the rule gets broken —
it IS the fix, and the prose is the workaround. Filing it would also have been a
grain of sand against ED-1550, whose live-session exemption is exactly the case
this was. E-2073 was reopened to `revisit` and extended in place.

`blockWorktreeRemovalIfApplicable` in `internal/hookcmd/claude.go`, alongside the
existing Bash gates. Refuses `endless worktree drop|reap`, `git worktree
remove|prune`, and `rm -r` of a worktree directory. No bypass flag: a session
that talks itself into "I was asked" talks itself into "this is the case the
bypass is for". Placed first among the Bash gates and ahead of the
`!isRegistered` early-return.

Scope per ED-1578 (proposed): EVERY agent session, not just worktree-bound ones.
Reverses the half of E-1947 that assigned removal to the spawning session. The
capability moves off the agent's tool path, not away: the person running the
session removes worktrees in their own shell; the reaper is a subprocess the
hook never sees.

Matching is anchored to a COMMAND position with quotes excluded, so mentioning a
removal is not performing one. The templates, the guide and this task's verify
script all quote these commands in order to forbid them.

Also required, and not optional: `setup_claude_hook` early-returns on finding
endless-go under ANY event, so a machine whose install predates an event never
gains it. `_repair_missing_hook_events` (src/endless/setup.py) fixes that.

## Verification

`tests/tasks/e-2073-verify.sh`, one command, 94 checks. Wiring is asserted from
source, NOT by running the hook: `endless-go hook` pins the main database
regardless of XDG_CONFIG_HOME, so every invocation writes there and a run from a
fixture directory auto-registers it as a project. The matcher is covered by
`TestWorktreeRemovalRes` (20 blocked routes, 20 allowed commands).

## Known blockers, not caused by this task

1. This machine has no `PreToolUse` entry in `~/.claude/settings.json`, so the
   gate — and every other PreToolUse gate — is inert until `endless setup
   claude-hook` is re-run. The installer fix above makes that re-run work.
2. The main database still carries the triggers
   `sessions_null_process_on_end_{insert,update}`, which reference `NEW.process`.
   That column is now `process_id`, so any write setting `state='ended'` fails
   outright. Reproduced on a copy. Unrelated to this task; it is what made the
   live end-to-end hook verification impossible.
