Settled by Mike (2026-10-07), not open:
- The goal is that an error reaching Mike is already being fixed. Every error that surfaces costs him a step backwards from work he thought was done.
- Auto-spawning a fix is wanted, including skipping the plan-approval gate: a spawned session almost always stops and presents open questions anyway.
- Attribution (E-2268) is the prerequisite for routing to a session.

## Q1. Which incidents does the job act on?
- A. Every new incident. Pro: nothing slips through. Con: acts on noise that ED-1614 says should never have been a fault (incident 1608 was a 15-minute outage).
- B. Every new incident, except codes the catalog marks as not actionable by a fix (e.g. job-unreachable, which clears itself). Pro: the catalog already knows each code's remedy; one flag per code. Con: a new catalog field to keep honest.
- C. Only incidents that recur past a threshold. Pro: one-off blips are ignored. Con: a real bug waits for its Nth occurrence.
Recommendation: B.

## Q2. Attributed to a live session: how does it reach that session?
- A. A session notice (the same queue as the "FYI —" task-change notices), delivered at the session's next prompt. No model call by the job; the session's own model acts on it. Pro: exists today. Con: an idle session sees it only when someone next prompts it.
- B. A notice, plus a nudge into an idle session's tmux pane (send-keys of a short prompt) so it takes a turn now. Pro: the fix starts without Mike. Con: typing into a pane can collide with a person typing there.
- C. Ignore the live session and spawn a new fix task. Pro: no interruption. Con: the session that has the context is bypassed.
Recommendation: B, nudging only a session whose state is idle.

## Q3. Attributed to a session that has ended
- A. Set the task that raised it to revisit and spawn a session on it. Pro: the fix lands where the bug was introduced. Con: reopens a task Mike may already have confirmed and landed.
- B. File a new bugfix task that cleans up the originating task, and spawn it. Pro: history stays truthful; the link still points at the origin. Con: one more task in the backlog.
Recommendation: B.

## Q4. Not attributed: file and spawn
- How are duplicates prevented? Recommendation: one task per fingerprint, linked to the incident; a recurrence updates that task rather than filing another.
- How many fix sessions can run at once? Options: reuse auto-spawn's per-project cap; a separate cap for fix sessions; no cap. Recommendation: reuse auto-spawn's cap so fixes and other work share one budget.
- What stops a loop (a fix raises a fault, which spawns a fix)? Recommendation: never spawn for an incident attributed to a session that is itself a fix session; record it on the originating fix task instead.

## Q5. How much diagnosis does the job do before handing off?
- A. None: hand over the incident id; the session runs errors show --detail itself. Pro: least to build. Con: every session starts by re-gathering the same evidence.
- B. Deterministic gathering only: the incident's detail, occurrences and attribution go into the task's context when filed. Pro: no model call in the job; the session starts informed. Con: none beyond A.
- C. A model call in the job to diagnose before filing. Con: spends model time on every incident, before knowing it needs a fix.
Recommendation: B.

## Q6. Opt-in
- A. Per-project config (fault_triage.enabled, like auto_spawn.enabled). Pro: matches the existing job opt-ins.
- B. On for every project. Con: another person's project starts spawning sessions without asking (PRODUCT).
Recommendation: A.

## Q7. A fault that is a reporting problem rather than a bug (ED-1614)
When the diagnosis is "this condition is routine and should not be a fault" (incidents 1542 and 1608 were both this), the fix is to the fault's producer, not to the reported condition.
Recommendation: no special case: the spawned session's task context cites ED-1614, so its plan can make that call.
