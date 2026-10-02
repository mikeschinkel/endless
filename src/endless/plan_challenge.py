"""The adversarial challenge a drafted plan must pass before it is attached (E-1994).

A primed session on a task with no plan drafts one. Without a check between,
the same agent would author the plan and then execute it with only the user's
approval in the way — and a self-authored plan is self-consistent, so a bad one
is exactly the kind that reads well to its author. This module is that check: a
one-shot headless model call, with no window, worktree, claim or session row,
that reads the drafted plan against the task it is for and either passes it or
says what is wrong.

Pure: the caller hands it the texts and gets a verdict back. It reads nothing
and writes nothing, so it is testable without a database or a model.

Fails CLOSED. A challenge that could not run — no `claude` binary, a timeout, an
unparseable reply — is a refusal that says so, never a pass. The whole point is
that a drafted plan does not reach `submitted` unchallenged; letting it through
when the challenger is unavailable would make the gate a no-op on exactly the
machines that cannot run it, silently. The drafting session can still ask the
user instead: an open question parks the task without any plan at all.
"""

from __future__ import annotations

import subprocess
from dataclasses import dataclass

# Large enough for a real read of a real plan; the call is made once per
# drafted plan, never on a hot path.
CHALLENGE_TIMEOUT_SECONDS = 180

PROMPT_TEMPLATE = """\
You are an adversarial reviewer. A coding agent has drafted an implementation
plan for a task, and it will also be the agent that implements it. Your job is
to catch a plan that should not be approved as written. Be skeptical: a plan
written by its own implementer reads as consistent to that implementer whether
or not it is right.

Judge ONLY the plan below against the task below. Fail it if any of these hold:
- it does not do what the task describes, or quietly does something else;
- a decision a person must make is made silently instead of left as an open
  question;
- it is too vague to implement from (no concrete changes, no way to tell it is
  done);
- it is internally inconsistent.

Do not fail it for style, length, or for choices that are reasonable but not
the ones you would make.

Reply with EXACTLY one of these, and nothing before it:
PASS
or
FAIL
- <objection 1>
- <objection 2>

=== TASK E-{task_id}: {title} ===
Type: {task_type}
Description:
{description}

Context:
{context}

=== DRAFTED PLAN ===
{plan}
"""


@dataclass(frozen=True)
class Verdict:
    """The challenge's answer. `objections` is non-empty exactly when it failed."""

    passed: bool
    objections: tuple[str, ...]


def build_prompt(*, task_id: int, title: str, task_type: str,
                 description: str, context: str, plan: str) -> str:
    """The prompt for one challenge. Empty fields render as `(none)`."""
    return PROMPT_TEMPLATE.format(
        task_id=task_id,
        title=title,
        task_type=task_type or "todo",
        description=description.strip() or "(none)",
        context=context.strip() or "(none)",
        plan=plan.strip(),
    )


def parse_reply(reply: str) -> Verdict:
    """Read a model reply into a verdict, failing closed on anything else.

    PASS must be the whole first line. FAIL must carry at least one objection —
    a bare FAIL gives the drafting session nothing to revise against, so it is
    reported as a failed challenge rather than accepted as an empty one.
    """
    lines = [ln.rstrip() for ln in reply.strip().splitlines()]
    if not lines:
        return Verdict(False, ("the challenge returned an empty reply",))
    head = lines[0].strip().upper()
    if head == "PASS":
        return Verdict(True, ())
    if head == "FAIL":
        objections = tuple(
            ln.strip().lstrip("-*").strip()
            for ln in lines[1:]
            if ln.strip().lstrip("-*").strip()
        )
        if objections:
            return Verdict(False, objections)
        return Verdict(False, ("the challenge failed the plan without saying why",))
    return Verdict(False, (f"the challenge's reply could not be read: {lines[0][:120]!r}",))


def challenge(*, task_id: int, title: str, task_type: str, description: str,
              context: str, plan: str, model: str | None) -> Verdict:
    """Run the challenge once and return its verdict."""
    from endless import internal_claude

    prompt = build_prompt(
        task_id=task_id, title=title, task_type=task_type,
        description=description, context=context, plan=plan,
    )
    try:
        result = internal_claude.run_internal_claude(
            prompt, model=model, effort="medium",
            timeout=CHALLENGE_TIMEOUT_SECONDS,
        )
    except FileNotFoundError:
        return Verdict(False, ("the challenge could not run: `claude` is not on PATH",))
    except subprocess.TimeoutExpired:
        return Verdict(False, (
            f"the challenge could not run: no reply within {CHALLENGE_TIMEOUT_SECONDS}s",
        ))
    if result.returncode != 0:
        detail = (result.stderr or result.stdout or "").strip()[:200]
        return Verdict(False, (
            f"the challenge could not run: claude exited {result.returncode}: {detail}",
        ))
    return parse_reply(result.stdout)
