# The threat model, stated first because an earlier version of this analysis got it wrong

**Gates and detectors here exist to catch INNOCENT INCORRECT USAGE, not errant
or adversarial agent behaviour.** (Mike, 2026-09-23.)

An earlier draft argued that the gate-escape regexes could be "spoofed open".
That framing is wrong and it inflated the apparent severity. Adversarial
resistance was never achievable and is not the goal: an agent that WANTED a
gate released could simply run the real command. Nothing here is a security
boundary.

The sufficient complaint is the innocent one, and it is entirely real: **an
agent writing documentation, a test, or a commit message that MENTIONS a
command triggers that command's effect by accident.** Everything below is
scoped to that.

# What actually happens

NOTHING IS EXECUTED. No shell runs the quoted or heredoc text; the tool call
does exactly what it says, and a heredoc body is written to a file as bytes.

The defect is MISATTRIBUTION, one step later:

1. PostToolUse hands the hook the tool input as JSON, where `command` is the
   WHOLE command string — heredoc body included, as one blob.
2. `handlePostToolUseSession` regexes that blob and concludes the agent ran the
   verb.
3. It then performs the effect ITSELF, in Go, by calling the monitor directly.

So the blast radius is exactly what those handlers do; arbitrary commands are
not reachable.

# The real problem: the hook writes state it does not own

This is the whole of the task. The other findings below are secondary.

| text names | hook calls       | effect                                            |
|------------|------------------|---------------------------------------------------|
| claim E-<n>   | StartWorkSession | BindSessionToTask + promote status to underway |
| confirm E-<n> | CompleteTask     | UPDATE tasks SET status='confirmed', completed_at |
| chat          | StartChatSession | session state                                     |

The claim one is the serious case: on a session whose `task_id` is NULL that
bind is the FIRST write to a write-once column (ED-1560 / E-1969), so it is
PERMANENT and `task bind` cannot move it afterwards.

## The hook's write is redundant, and predates the code that owns the job

Verified, and this is what makes the fix small:

- `endless task claim` resolves its session and emits `task.claimed`;
  `execTaskClaimed` does `UPDATE sessions SET task_id = ?`. The CLI and the
  event executor already perform the binding.
- The hook's `StartWorkSession` call is much OLDER than that executor. Its
  earliest trace is the early "status page / plan system" era;
  `execTaskClaimed` arrived later with E-1242 ("bind sibling-pane Claude
  session on 'task claim' from CLI shell").
- In the happy path the hook therefore writes the same value the executor just
  wrote, which the write-once trigger permits because
  `NEW.task_id IS NOT OLD.task_id` is false. A silent no-op.

So it is a legacy duplicate writer whose input can name a DIFFERENT id. That is
the entire danger, and it is why deleting it is viable rather than merely
desirable.

## It is also wrong in a way that has nothing to do with quoting

    endless task claim E-<digits> --unattended    →  matches, captures the id

`--unattended` (E-2093) means "claim with NO Claude session bound" — manual work
at a terminal, cron. Run it as a Bash tool call from a Claude session and the
hook binds the session anyway, overriding the flag. No heredoc required. E-2110
("Explore what an Endless session is without Claude and without tmux") is the
task that cares about this case.

## Session resolution is NOT the weak link — an earlier draft claimed it was

`_current_endless_session_id`'s ladder is not evidence of unreliability. Rung 2
(`CLAUDECODE=1` + `CLAUDE_CODE_SESSION_ID`) is IN-PROCESS: the running process
IS the Claude pane, so for a claim issued by a Claude session there is no
guessing at all. Rungs 3-5 exist for callers that are not Claude — a human in a
shell pane, a sibling pane, cron — not as fallbacks for a flaky rung 2.

The earlier draft's proposal to give the CLI "an unforgeable session identity"
is therefore WITHDRAWN: that identity already exists and is already used.

# Secondary: the PreToolUse escapes

Two gates carried an escape-verb regex so they would never block the command
they tell you to run. **One of them is already gone.** E-1983 was reopened on
2026-09-23 and narrowed to write tools; because Bash is no longer gated the
remedy always runs, so `bindEscapeVerbRe` was deleted rather than hardened.
That is the shape of fix this family wants: remove the reason for the carve-out,
not tighten the regex.

What remains is `revisitClearVerbRe` on E-1542's pause-on-revisit gate. Verified:
`echo "endless task continue"` matches it. The consequence is MILD and must not
be conflated with the state writes above — the decision is stateless, so only
that one tool call slips through and the next is blocked again. The gate's
persistent state is cleared only when the epic leaves `revisit` or by an
explicit session command; text never reaches it.

Whether that is worth changing is a judgment call about one gate, not a
requirement of this task.

# Secondary: the gate-applying detectors

Prose quoting a forbidden command (the commit-on-main gate, the
sqlite-on-the-endless-db gate, the landed-suite run gate) is refused as though
it were one. A nuisance, not a state change.

# Why one rule will not serve every call site

The families have opposite failure economies, which is why "tighten the regex"
is not a global answer:

- **State writes** — a false negative is CHEAP (the CLI already did the write);
  a false positive is a wrong row, sometimes irreversible. Wants the strictest
  possible treatment, up to not matching text at all.
- **Gate escapes** — a false negative BLOCKS the agent from its own remedy. This
  is the family where tightening is the dangerous direction, and where removing
  the need for an escape beats improving the match.
- **Gate applications** — a false positive is a nuisance, a false negative lets
  the blocked thing through. Lowest stakes.

# Recommendation

**Delete the state writes from the hook; keep the handoff render.** The CLI and
the event executor own the write. What the hook keeps is E-1822's
claimed-into-a-live-session handoff message, where a false positive costs a
stray paragraph of context and nothing else.

Considered and rejected:

- **Tighten the text match** (command-position parsing, stripping heredocs and
  quotes). Still inference, and Bash is not parseable by a small helper —
  pipelines, `&&`, `$( )`, `bash -c`, env prefixes, `uv run`, aliases; heredoc
  stripping is itself a parser. It lowers probability without changing kind, in
  the one family where a false negative is cheap anyway.
- **Verify before writing** (check for a matching `task.claimed` event first).
  If the event exists the hook's write is redundant, so this collapses into the
  recommendation above; the intermediate step buys nothing.
- **A bespoke receipt channel.** The event ledger already is one.
- **Give the CLI an unforgeable session identity.** Withdrawn — it has one.

Open for whoever plans it: whether the handoff render should keep matching text
at all, or key on the same signal once state writes stop depending on it.

The population to review is whatever `grep -rn 'input.Command' internal/hookcmd/`
returns outside tests.

Not the same as E-1838: that is the inline-content path gate on task description
text — a different detector on a different surface, already underway on its own
rule. Same class, not the same bug.
