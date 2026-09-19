# E-1962 — implementation record

## The empirical finding

Claude Code Desktop told Mike `CLAUDE_CODE_ENTRYPOINT` "does not exist". It does.
Both harnesses were dumped on 2026-08-13:

| var | Claude Code, terminal (2.1.222) | Claude Code Desktop |
|---|---|---|
| `CLAUDE_CODE_ENTRYPOINT` | `cli` | *absent* |
| `CLAUDECODE` | `1` | *absent* |
| `AI_AGENT` | `claude-code_2-1-222_agent` | *absent* |
| `CLAUDE_AGENT_SDK_VERSION` | *absent* | `0.3.222` |
| `__CFBundleIdentifier` | `com.apple.Terminal` | `com.anthropic.claudefordesktop` |

Desktop hosts the agent through the **Agent SDK**, not the CLI, so none of the
CLI's own variables reach a subprocess there. That gap is the discriminator.

Directly useful to **E-1505**, which already guessed at this: it calls for
`platform='claude-desktop'` on the session row and names `AI_AGENT` as the
signal. `AI_AGENT` turns out to be absent on Desktop, so it cannot carry that;
`agentenv.Detect()` now can.

## What shipped

### `internal/agentenv` (new package)

Answers "which agent harness is running Endless, and do we support it?" — an
identity plus a support flag, not a boolean, because the shape has to hold for
Codex CLI and whatever comes next.

- `Detect() ID` → `claude_cli` | `claude_desktop` | `unknown`
- `Supported() bool` → allow-list; today `{claude_cli}`
- `Label(ID) string` → human phrasing for the guide banner
- `DetectWith`/`SupportedWith` take a `Lookup` so detectors are testable without
  mutating the process environment.

Named `agentenv`, not `agent`: Endless already calls the background workers under
an epic "agents".

**Detector table is the extension point** — a new harness is one row plus an ID
constant. Deliberately **no Codex CLI row yet**: a detector never checked against
a real `env` dump of that harness is a guess, and a guess fails silently (the
harness is misidentified and the support flag lands wrong either way). The
missing input is a dump, not code.

**Ordering matters** and is pinned by a test: `claude_cli`'s positive match runs
first, because `claude_desktop` is detected partly by ABSENCE. A terminal session
that happens to carry an SDK version stays `claude_cli`.

`claude_desktop` uses two signals with honest limits — the bundle id is proof but
macOS-only; the SDK version is portable but really means "an Agent SDK hosts
this". Neither is load-bearing while everything except `claude_cli` is
unsupported; both should be re-derived from a fresh dump under E-1505.

`src/endless/agent_env.py` mirrors it, thin, for `endless guide`. The Go side is
the one that enforces; a test asserts the two tables have not drifted.

### What is gated

**The Claude hooks** — `reportChannelOn` in `internal/hookcmd/claude.go` gained
`supportedAgent()`. Placement is the point: that one function feeds the
SessionStart rule, the PostToolUse reinforcement, AND the Stop gate, so E-1953's
told-iff-gated invariant holds automatically. Harness and `report_gate` are
**independent vetoes** — the product's decision and the project's — never an
override: a terminal session in a `"report_gate": false` project stays off.

`internal/hookcmd/claim_handoff.go` — same AND. It renders in the *claiming
session's own hook*, so the environment read is that session's.

**`endless guide`** — an unsupported harness gets a banner telling it to ignore
Endless (naming E-1505), and the guide body is withheld: printing it would
contradict the banner in the same breath, since the guide's entire content is
instructions.

Nothing else. A hook fires inside the session and speaks to the agent, which is
where an unsupported harness gets mis-instructed. A user running `endless task
add` from Desktop is a person using a tool.

### The one deliberate asymmetry

The guide banner fails **open** on `unknown`; the hooks fail **closed**. `unknown`
is overwhelmingly a human at a shell prompt — the docs tell you to run `endless
guide` — and refusing them breaks a real workflow to defend against a harness
that may not exist. Getting it wrong here costs an unrecognized agent one guide
it should have ignored; the other way costs every human the guide.

## Deliberately NOT changed

`src/endless/task_cmd.py:_handoff_report_gate()` (the **spawn** handoff).
`endless task spawn` opens a tmux window, so the session it describes is a
terminal Claude Code one by construction whatever harness ran the command.
Gating it on the caller's environment would strip the contract from terminal
sessions spawned from Desktop. The verify script asserts `task_cmd.py` contains
no harness detection, so this stays deliberate rather than drifting.

## Deferred

Project-configurable harness support (`report_gate_surfaces` or similar) waits on
**E-1505** — there is no second supported harness to configure until then. Linked
`relates_to`.

## Verification

`./tests/tasks/e-1962-verify.sh` — 23 checks, all passing. Both harnesses are
simulated by environment alone, which is not a stub but a reproduction of the
observed conditions.

- Part 0: build, vet, `go test ./internal/agentenv/...` + `./internal/hookcmd/...`,
  `pytest tests/test_agent_env.py` (fail-fast).
- Part 1: all three hook consumers, real binary → sandbox DB. Terminal injects
  the rule / blocks an unreported reply / fires the reinforcement; Desktop silent
  on every one.
- Part 2: terminal + `report_gate:false` → still off (veto, not override).
- Part 3: claim handoff gated; Python spawn handoff provably left alone.
- Part 4: guide banner on Desktop; guide body withheld; terminal and bare-shell
  both still print.

Project-wide: `go build/vet/test ./...` clean; `just test` 1374 passed (+17).

## Corrections made mid-task

Two things I did wrong and reverted after Mike's correction:

1. Edited `tests/tasks/e-1953-verify.sh` to keep it passing, and had this suite
   delegate to it. **Verify scripts are pre-land gates for their own task in
   their own worktree, not a regression suite.** Both reverted; the rule is now
   written into CLAUDE.md's Tests section.
2. Filed E-1965 about `e-1901`/`e-1822` verify-script failures. Declined —
   invalid premise, those already landed and I should not have run them.

## Related

- **E-1505** add support for Claude Desktop — `relates_to`. Consumes
  `agentenv.Detect()` and is where the support flag flips.
- **E-1966** the Python wind-down nudge ignores `report_gate` (config-key half
  only; plan attached, `submitted`).
