# The failure is silent, which is why it survived a landing

`hook claude` returns immediately on an unrecognized agent harness (E-1962):
exit 0, no stdout, no side effects. A bare shell is unrecognized.

Any verify script that impersonates a session by piping a payload into
`endless-go hook claude` therefore stopped exercising anything the moment
E-1962 landed. Nothing announced it, because roughly half of what such a
script asserts is that the hook stays SILENT — a turn allowed to end, a
gate-off project, a subagent's return value, a `$FULL` license. Every one of
those passes against a hook that does nothing at all.

So the suite does not fail. It hollows out, and keeps reporting green.

## Confirmed instance

`tests/tasks/e-1953-verify.sh` pipes into `go_sandbox hook claude` at three
call sites and never exports `CLAUDE_CODE_ENTRYPOINT`. It passes from inside a
Claude session, which exports that variable to its subprocesses, and is hollow
from a plain terminal — which is where a pre-land gate is actually run.

## How it was found

`tests/tasks/e-1975-verify.sh` had the identical bug. 106/106 from inside a
session; 89 passed / 16 failed from a bare shell. Every failure was an
assertion that needed the hook to DO something; every assertion expecting
silence passed. That asymmetry is the signature.

E-1975 fixed its own script two ways, both of which apply here:

  * every hook invocation goes through a helper that sets
    `CLAUDE_CODE_ENTRYPOINT=cli`;
  * a preflight fires one payload that MUST produce a block and fails loudly if
    it does not, so a dead hook fails at the top rather than hollowing out the
    body.

## What to decide, not just what to fix

Fixing E-1953's script is mechanical. The open question is whether driving the
hook belongs in a shared helper rather than being re-derived per script. The
failure mode is silent and the precondition is invisible in the script itself,
so the next author will reproduce it — as this one did, in a script written
specifically to be careful about it.
