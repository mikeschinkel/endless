2026-10-11, from E-2166's session: the INSTALL half of this task shipped as
`endless setup claude-hook` (src/endless/setup.py). The VERIFY half named in
this task's own description did not, and that is the remaining gap.

What is already there: CLAUDE_HOOK_EVENTS lists eight events; SYNC_EVENTS marks
the five that must run synchronously; `_repair_missing_hook_events` and
`_repair_hook_async_flags` already add what is missing and fix the flags, and
both run on every `setup claude-hook`. The handler side is complete too — every
one of the eight has a handler in internal/hookcmd.

What is missing: nothing ever RUNS that comparison unprompted. The repair only
happens if someone types the setup command, so a user scope that drifts behind
the code's intended set stays drifted silently.

Observed, not hypothesised: on this machine two of the eight (StopFailure,
Notification) were simply absent from the user scope until a manual
`endless setup claude-hook` on 2026-10-11 added them. StopFailure is the
expensive one — Stop and StopFailure are alternatives (E-2145), so without it a
turn that dies on an API error leaves the session reading `working`, and both
`project monitor` and auto-spawn's concurrency cap treat `working` as a live
claim. The symptom is throttled spawning with no error anywhere.

So the scope this leaves: compare the installed hook set against
CLAUDE_HOOK_EVENTS and surface the difference without being asked. Fixing it is
already a solved problem; noticing it is not.
