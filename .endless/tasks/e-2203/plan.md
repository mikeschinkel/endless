# Rater job, agent-forced ratings on plan attach, approve wording

Settled with Mike in ES-1248 (2026-09-30). Ratings are the agent's to know;
the user ratifies and should never have to originate them.

## 1. Agents must rate when a plan promotes a task

When an AGENT's `task add` / `task update` attaches a plan that would promote
the task to `submitted`, both ratings must be present (on the call or already on
the task). Missing → the PROMOTION is refused with the exact flags; the plan
itself is still written and the task stays at its current status. Detect the
agent with `agent_help.agent_facing()`. A human attaching a plan is not refused.
`task submit` already enforces this.

## 2. A `rater` job for unrated submitted tasks

A registered job on the E-698 runner, `JobName = "rater"`, sweeping `submitted`
tasks with either rating unset — in practice human-filed ones. Per task: render
a template (`rater/ratings`, three-layer lookup like the old triage template),
one model call, parse `COMPLEXITY:` / `RISK:` leniently, emit
`task.fields_updated` for unset axes only, `actor.kind = triager` (the class of
job; `rater` is the job). Fail-open: no verdict leaves the task unrated and
records a fault. Per-task claim before the model call, as the old triager had.
Reuse what E-1993 deleted from triage (render, claim, fault reporting) from
history rather than re-deriving it.

## 3. Approve's refusal addresses the right person

`task approve` on an unrated task says the ratings were never proposed and that
the agent (or the rater job) proposes them; `--complexity`/`--risk` are named
as the override, second.

## Verify

Unit: the plan-attach refusal for agents only, and the plan still written;
rater parse, unset-axes-only, fail-open, claim. Live: a human-filed task with a
plan gets rated by one `endless jobs run` with a stub model.
