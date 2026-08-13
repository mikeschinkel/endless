The report channel (E-1953) is delivered to, and enforced against, every Claude
Code session on a registered project whose nearest `.endless/config.json` leaves
`report_gate` on. That is too wide: it also catches sessions running in the
**Claude Code Desktop app**, where the `endless task report` contract is not
currently wanted. The desktop session gets told to route its reply through
`endless task report`, and (if a Stop hook fires there) can be blocked for not
having done so.

Intent: the report channel is a **terminal-only** contract for now. Endless must
be able to tell a terminal CLI session from a Desktop-app session and gate on it.

## Where it is wired

- `internal/hookcmd/claude.go` — `reportChannelOn(projectID, isRegistered, cwd)`
  is the single switch, used deliberately by BOTH halves:
  - `handleTaskContextInjection` → `composeSessionStartContext(..., reportChannelOn(...))`
    injects `reportChannelRule` on SessionStart, and
  - the `Stop` case → `enforceReportGate(...)`, which blocks the turn.
  The comment on `reportChannelOn` states the invariant to preserve: a session
  must never be told to use a channel that will not gate it, nor gated without
  having been told. So the surface detection belongs inside `reportChannelOn`,
  not bolted onto one of the two call sites.
- `internal/monitor/db.go` — `ReportGateEnabledForCwd` / `ReportGateEnabled`
  resolve the per-project `report_gate` key (nearest-config-wins). Surface
  detection is a separate axis from that config key and should compose with it
  (both must say yes).
- `src/endless/config.py:project_report_gate` mirrors the Go resolution.

## The detection signal

CC Desktop told Mike that `CLAUDE_CODE_ENTRYPOINT` "does not exist". That is
wrong. Verified empirically from a terminal session on 2026-08-13 (Claude Code
2.1.222): the hook subprocess environment contains

    CLAUDE_CODE_ENTRYPOINT=cli
    CLAUDECODE=1
    CLAUDE_CODE_CHILD_SESSION=1
    CLAUDE_CODE_SESSION_ID=...
    CLAUDE_CODE_EXECPATH=...
    AI_AGENT=claude-code_2-1-222_agent
    TERM_PROGRAM=tmux            # incidental — Mike's terminal, not a CC signal

`endless-hook` already reads process env directly (`TMUX_PANE`,
`CLAUDE_JOB_DIR`), so reading one more var is in-pattern.

**Unknown that must be resolved before implementing:** what
`CLAUDE_CODE_ENTRYPOINT` (and the rest of the env) actually contains inside a
Claude Code Desktop session. Do not guess a value. Capture it empirically — have
a Desktop session dump its env (or land a temporary diagnostic path in the hook)
and record the observed values in this task before coding the predicate.

Design consequence of that unknown: the predicate should **allow-list the
terminal**, not deny-list the desktop. `CLAUDE_CODE_ENTRYPOINT == "cli"` → gate
on; anything else (desktop, vscode, sdk, remote, unset) → gate off. Failing
closed on an unrecognized surface matches the rest of the gate's design (it
already returns false for unregistered projects and unresolvable paths) and
means a future Claude Code surface does not silently start enforcing the channel.

Sub-question to settle: whether the terminal-only rule is hardcoded or itself a
config key (e.g. `report_gate_surfaces`). Prefer hardcoded + documented until
there is a second consumer — Mike's stated intent is "currently" terminal-only,
which is a policy that may widen later, not a per-project knob.

## Notes

- Endless's own checkout has `"report_gate": false`, so the gate is off here and
  the fix cannot be observed from inside this repo. Verification needs a
  registered project that leaves the key unset, driven once from the terminal
  (rule present) and once from Desktop (rule absent).
- `CLAUDE_CODE_ENTRYPOINT` is not part of the hook stdin payload — it is process
  env only. If the payload ever grows a surface field, prefer that.
- Related but distinct: `SessionStart` matchers (`startup`/`resume`/`clear`/
  `compact`/`fork`) describe how a session began, not what surface it runs on;
  they are not a substitute here.

## Done when

- A single predicate in `internal/hookcmd/claude.go` decides surface, consulted
  by `reportChannelOn` so injection and enforcement can never disagree.
- Observed Desktop env values recorded in this task.
- Go tests cover: terminal → on, desktop → off, unset → off, and terminal +
  `report_gate: false` → still off.
- Docs updated where the gate is described: `CLAUDE.md` ("Reporting to the user
  — the minimizer gate"), `docs/guide/tasks.md`.
