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



## As built (settled during implementation)

- **`task add` is refused up front** (Mike, this session): an agent's `task add
  --plan` with a rating unset files nothing and names the missing flags, so a
  re-run cannot duplicate the task. `task update --plan` writes the plan, holds
  the status, and exits non-zero naming `task submit E-N --complexity … --risk …`.
- **Epics are exempt** from the plan-attach refusal: `epic add`/`epic update`
  carry no rating flags, and an epic's status is derived from its children.
- **Claim table restored as `rater_claims`** (migration 00012; schema.sql
  declares it). A hand-run `endless rater run` never enters the job lease, so
  the per-task claim is what keeps it and the sweep from both paying.
- **New fault code WARN-0021 `rate-failed`**, raised for no verdict and for a
  partial one (one axis written, the other still missing). Retired WARN-0009 is
  not reused.
- **`endless rater run`** (`--task`, `--limit`, `--project`, `--all-projects`,
  `--dry-run`) is the job's subprocess and the manual retry. Model purpose
  `rater`, default `sonnet`. Event provenance: `payload.rater = {job, model,
  template}`; `ActorTriager` doc updated — it is emitted again.
- The human-facing plan-attach note now says the rater job proposes the
  ratings instead of asking the person to originate them.
