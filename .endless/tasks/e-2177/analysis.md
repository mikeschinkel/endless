Every Bash-command detector in the Claude hook regex-matches the RAW command
string, with no sense of shell quoting. A verb named inside a quoted argument or
a heredoc body reads exactly like a verb being run, so writing ABOUT a command
is indistinguishable from running it. Ten call sites in internal/hookcmd share
this, and they fail in two opposite directions.

DIRECTION 1 — a spurious state write. handlePostToolUseSession matches the
`claim` action regex and treats the match as a real claim. Observed twice in one
session: a Bash call whose heredoc wrote a Go test file containing the literal
`endless task claim E-1983` injected the entire claimed-into-a-running-session
handoff mid-task. It fired a third time while probing the bug, from the probe's
own heredoc. Harmless there only because the session already held that task; a
different id in the text is a claim the agent never made.

DIRECTION 2 — a gate is wrongly RELEASED, which is worse in kind. Two gates
carry an escape-verb regex so they never block the command they tell you to run,
and both can be spoofed open by TEXT:

    echo "endless task claim E-1"    -> bindEscapeVerbRe    matches (verified)
    echo "endless task continue"     -> revisitClearVerbRe  matches (verified)

A single tool call carrying that string passes the unbound-worktree gate
(E-1983) or the pause-on-revisit gate (E-1542) without binding or continuing
anything. Neither gate is load-bearing against an adversary — the agent could
simply run the real command — but both are load-bearing against ACCIDENT, and an
agent writing documentation or tests about these verbs releases them without
knowing it.

The same blind spot also produces spurious REFUSALS in the gate-applying
detectors (the commit-on-main gate, the sqlite-on-the-endless-db gate, the
landed-suite run gate): prose quoting a forbidden command is refused as if it
were one.

This is filed as the CAUSE, not as the three symptoms. It is not the
inline-content path gate of E-1838 — different detector, different surface, and
that one is underway on its own rule.

What a fix has to decide, since it is not obvious: whether to parse the command
into words and match only what is in COMMAND POSITION, whether to strip heredoc
bodies and quoted strings before matching, or whether the escape-verb gates
should stop reading text at all and key on something the shell cannot forge.
The three call-site families have different tolerances — a missed claim is
cheap, a wrongly-released gate is not — so one rule may not serve all ten.

The population is whatever `grep -rn 'input.Command' internal/hookcmd/` returns
outside tests — ten sites at the time of filing, in claude.go and
verify_suite.go. The two verified spoofs above are bindEscapeVerbRe and
revisitClearVerbRe; the spurious claim runs through handlePostToolUseSession via
matchers.ActionRegex.
