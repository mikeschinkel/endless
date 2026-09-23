# What actually happens, stated precisely

NOTHING IS EXECUTED. This is worth saying first because the shape invites the
opposite reading. No shell ever runs the quoted or heredoc text; the tool call
does exactly what it says, and the heredoc body is written to a file as bytes.

The defect is MISATTRIBUTION, one step later:

1. PostToolUse hands the hook the tool input as JSON, where `command` is the
   WHOLE command string — heredoc body included, as one blob.
2. `handlePostToolUseSession` regexes that blob, and on a match concludes the
   agent ran the verb.
3. It then performs the effect ITSELF, in Go, by calling the monitor directly.

So the blast radius is bounded by exactly what those handlers do, and arbitrary
commands are not reachable through it.

Why the hook infers at all: the harness never tells Endless "the agent claimed a
task". Endless has to notice by watching Bash commands go past. Inference from
command text is the design. The defect is that the inference has no notion of
COMMAND POSITION versus quoted data.

# The three inferred actions, and what each one writes

| text names | hook calls        | effect                                             |
|------------|-------------------|----------------------------------------------------|
| claim E-<n>   | StartWorkSession  | BindSessionToTask + promote status to underway   |
| confirm E-<n> | CompleteTask      | UPDATE tasks SET status='confirmed', completed_at |
| chat          | StartChatSession  | session state                                     |

The claim one is the serious case. On a session whose task_id is NULL, that bind
is the FIRST write to a write-once column (ED-1560 / E-1969), so it is PERMANENT
and `task bind` cannot move it afterwards. That is E-1983's failure mode
arriving through a different door: E-1983 removed the tmux window option's
ability to mis-bind a session, and this reaches the same irreversible write from
command text instead.

The confirm one closes someone's task on the strength of prose.

## Verified

Against the shipped patterns, both captured the id from a quoted argument AND
from a heredoc body:

    endless\s+task\s+claim\s+(?:[Ee]-)?(\d+)
    endless\s+task\s+confirm\s+(?:[Ee]-)?(\d+)

Observed live four times in one session: a Bash call whose heredoc wrote a Go
test file mentioning a claim command injected the whole
claimed-into-a-running-session handoff and ran StartWorkSession. It fired again
from the heredoc of the probe investigating it, and again from the `task add`
that filed this task — whose own analysis text contained an example. Harmless
each time only by accident: the id was either the task the session already held
(refused as a write-once reassignment) or an id with no row.

That accident is not a mitigation. The same text naming a real, unheld id
performs the write.

# The PreToolUse half is real but MILDER — do not conflate them

An earlier framing of this called the gates "spoofed open". That overstates it,
and the correction matters for how the fix is scoped.

Two gates carry an escape-verb regex so they never block the command they tell
you to run — the unbound-worktree gate (E-1983) and the pause-on-revisit gate
(E-1542). Both decisions are STATELESS with respect to that escape: the command
is re-read on every PreToolUse and nothing is recorded. So a call carrying the
text passes, and the very next call without it is blocked again. The revisit
gate's persistent state is cleared only when the epic leaves revisit or by an
explicit session command; text never reaches it.

Verified: `echo "endless task claim E-<digit>"` matches bindEscapeVerbRe, and
`echo "endless task continue"` matches revisitClearVerbRe.

So the gate half is "this one call slips through", not "the gate is now off".
Neither gate is load-bearing against an adversary anyway — the agent could
simply run the real command. They are load-bearing against ACCIDENT, and an
agent writing docs or tests about these verbs weakens them without knowing.

The gate-APPLYING detectors have the mirror-image nuisance: prose quoting a
forbidden command (the commit-on-main gate, the sqlite-on-the-endless-db gate,
the landed-suite run gate) is refused as though it were one.

# Scope, and what a fix has to decide

Filed as the CAUSE. The population is whatever
`grep -rn 'input.Command' internal/hookcmd/` returns outside tests — ten sites
at the time of filing, across claude.go and verify_suite.go.

Not the same as E-1838: that is the inline-content path gate on task
description text — a different detector on a different surface, already underway
on its own rule. Same class, not the same bug.

The three families have different tolerances, so one rule may not serve all ten:

- A missed claim is cheap (the agent re-runs it); a wrongly-performed claim is
  irreversible. This family should be the strictest.
- A gate escape that fails closed just means the agent runs the real command.
- A gate that fails open is the one to avoid.

Open questions for whoever plans it: parse the command into words and match only
what is in COMMAND POSITION; or strip heredoc bodies and quoted strings before
matching; or stop inferring state writes from text altogether and key them on
something the text cannot forge — the claim's own exit status, or having the
`endless` command itself report what it did. The last is the only one that is
robust rather than merely better, and it is also the largest.
