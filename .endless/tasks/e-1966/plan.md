# E-1966 — plan

Scope decided by Mike 2026-08-13: reimplement `_running_under_agent` on
`agent_env`, and add the missing `report_gate` check to the wind-down nudge.

## Two defects, one root

### 1. `_running_under_agent()` is a crude precursor of `agent_env`

`src/endless/task_cmd.py:228`:

```python
def _running_under_agent() -> bool:
    """True if invoked from an LLM agent harness.

    Today: Claude Code (sets CLAUDECODE=1). Extend as other harnesses are
    encountered. Used only to surface a stronger anti-rationalization variant
    of the verb-gate error — never to gate behavior.
    """
    import os
    return os.environ.get("CLAUDECODE") == "1"
```

Harness detection is its *only* job — and it is exactly the job
`src/endless/agent_env.py` now does properly (E-1962). Keeping both means two
answers to one question, and the `CLAUDECODE` one is the worse answer: it says
"some Claude Code", not which, and it misses every non-Claude harness the
docstring's own "extend as other harnesses are encountered" anticipates.

**The docstring is stale, not a constraint.** Written in E-1106 (2026-05-02),
when the function had exactly one caller: the verb-gate error message. E-1772
later added the wind-down nudge, which *does* gate behavior, and the docstring
was never updated. Do not treat "never to gate behavior" as a rule to preserve —
it is a description that stopped being true and should be rewritten.

Fix — **keep the function, delegate the answer.** It is a good convenience
wrapper: it names the question its callers actually ask, in their vocabulary, and
it is the seam where a future caveat would go. What it should not do is own a
second, worse implementation of harness detection.

```python
def _running_under_agent() -> bool:
    """True if invoked from an LLM agent harness (any recognized one)."""
    from endless import agent_env
    return agent_env.detect() != agent_env.UNKNOWN
```

Do NOT inline `agent_env.detect() != UNKNOWN` at the call sites. Two callers
today, and the wrapper is what keeps "am I talking to an agent?" from being
spelled three different ways.

Note the body is `detect() != UNKNOWN`, **not** `supported()`. The two callers want
"am I talking to an agent at all" — a verb-gate error phrased for an agent, and a
nudge aimed at an agent — not "is this harness supported". Since E-1962 the CLI
refuses unsupported harnesses at the group callback anyway, so by the time either
caller runs, a recognized harness is a supported one.

Check whether `agent_view_requested()` (`endless/agent_help.py`) overlaps; if it
is a second spelling of the same question, fold it in rather than leaving three.

### 2. The wind-down nudge ignores `report_gate`

Same file, the function guarded by `_is_report_wind_down` (~line 2944). It
prints:

```
▸ Report through the command, not prose. Route this handoff — and all
  further status reporting for the rest of this session — through:
      endless task report E-NNNN --draft-file <path>
```

Guarded only on `_running_under_agent() or agent_view_requested()`. It never
consults `project_report_gate()`, so it fires in projects that switched the
channel off — observed firing during E-1962's own `task update --status
unverified` inside Endless's own checkout, which ships `"report_gate": false`.

"The `report_gate` check" means exactly this: the per-project
`.endless/config.json` key, resolved nearest-first, that says whether the report
channel is live for this project. `config.project_report_gate(root)` is the
existing reader; `_handoff_report_gate()` in the same file is already a working
call site to copy.

This is the defect E-1953 fixed once on the Go side —
`TestReportReinforcement_RespectsTheSwitch` in
`internal/hookcmd/claude_report_test.go` — whose comment names what is at stake:

> An instruction may outlive its enforcement harmlessly ("send it verbatim" is
> still reasonable advice with no gate behind it). A factual assertion about
> enforcement may not: a session told it is being checked when it is not learns
> that Endless's statements about its own behavior cannot be relied on.

The Python emitter was missed in that sweep.

Fix — after the existing guards:

```python
from endless import config
root = config.enclosing_project_root()
if root is not None and not config.project_report_gate(root):
    return
```

`root is None` **fails open**, matching `_handoff_report_gate()`'s documented
reasoning: the channel ships on, and an unresolvable root is ignorance rather
than an opt-out.

`_handoff_report_gate()` already is those three lines. Reuse it rather than
writing a second copy; its name wants widening (it is no longer handoff-specific).

## Verification

- pytest: the nudge is silent when the enclosing project sets
  `"report_gate": false`, still fires when unset or true.
- pytest: `_running_under_agent()` is true for the Claude CLI environment and
  false for a bare shell, driven through `agent_env`'s detection rather than
  `CLAUDECODE` directly.
- Manual, from this repo (ships the gate off):
  `endless task update E-NNNN --status unverified` prints no report nudge.

## Do not

Do not fix the nudge by softening its wording so the claim becomes true-ish. The
sentence is correct where the gate is on; the bug is that it is emitted where the
gate is off.
