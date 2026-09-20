## The problem

Endless's refusals exist to spend a guardrail's attention instead of Mike's. An
agent that reports the refusal anyway spends his attention twice — once on the
guardrail, once on being told about it — which is strictly worse than not having
the guardrail.

Observed 2026-09-15: a write was refused for containing an absolute path. The
agent complied, rewrote it, and then reported the refusal in its handoff. Asked
whether an explicit "DO NOT REPORT THIS REFUSAL TO THE USER" in the message would
have stopped it, the answer was yes — which is the finding. The reasoning was
available without the instruction and the agent did not apply it, so the message
itself is the reliable place to carry the rule.

## Population to inventory

Counted 2026-09-15, as a starting point, not a specification:

| Surface | Count |
|---|---|
| `ClickException` raises (Python) | 423 |
| `click.echo(..., err=True)` | 91 |
| `Fprint*(os.Stderr, ...)` (Go) | 295 |
| `doterr`/`errors.New` sentinels (Go) | 177 |
| files carrying hook refusals | 12 |

Overlapping and not all user-facing; the count is there so the research knows
the order of magnitude before it starts.

## The classification test

Not "is this an error?" but: **after the agent complies, does the user's outcome
differ from what it would have been?**

- **Silent.** The guardrail fired, the agent had an obvious compliant
  alternative, and the result is identical. The absolute-path refusal is this:
  the content was rewritten project-relative and nothing about the task changed.
  Reporting it is narration.
- **Report.** The refusal blocked the work, changed what the user gets, needs a
  decision only they can make, or revealed a defect. The worktree-removal refusal
  is this — the agent must stop and say so.
- **Ambiguous.** Both, depending on whether a compliant alternative existed.
  These are the interesting ones and the research should name them individually
  rather than picking a default.

A refusal that is silent-when-complied-with but report-when-blocking is one
message with two audiences; whether that needs two messages or one conditional
marker is a finding, not an assumption.

## Mechanism, decided (Mike, 2026-09-15)

**Audience-aware rendering.** The directive reaches the agent and never the human.
Not a finding — do not re-open it.

The audience is an agent when the environment says so (`agent_env.present()` /
Go `agentenv.Present()`), or `--agent` was passed, or `--agent-view` was passed.
`--llm` is retired and superseded by `--agent`. The plan carries the detail,
including that `agent_facing()` does not yet honour `--agent`.

Rejected: literal directive text in the message (prose an agent may paraphrase, and
every human reads an instruction addressed to someone else); and a separate
machine-readable marker (a second audience mechanism beside the one that exists).

## Deliverable

The inventory with each user-facing refusal classified silent / report /
ambiguous, the recommended mechanism, and then a FILED todo task to apply it.
This task does not change code.



## Method

Eleven read-only audits ran in parallel, one per slice of the source, against one
written brief carrying the rule, the owner's calibration examples, and a fixed
TSV schema. Each audit read the code around every site rather than classifying
from the message string. Afterwards every REPORT row and every CONDITIONAL row
was re-read for consistency across slices, and the Python candidate sites
(every `ClickException`/`UsageError` construction, `err=True` echo, stderr
write and non-zero exit) were cross-checked against the rows: all are covered,
several through the row for the helper that builds the message.

`tension:` notes and side findings in the TSV are the auditors' readings. The
ones promoted into this page's *message defects* and *defects found* sections
were re-verified against the code.

A row is one message-construction site. A helper that builds a message raised
from several places is one row, with the raise sites in `notes`. An echo
followed by an exit is one row citing both lines.

### Columns

| Column | Meaning |
|---|---|
| `file` | repo-relative path |
| `symbol` | enclosing function, method or class — `(module level)` / `(package level)` where there is none |
| `lang` | `py` or `go` |
| `construct` | raise, usage, echo-err, exit, stderr, hook-block, hook-json, hook-context, log, error-return, helper |
| `audience` | `cli` (whoever ran `endless …`), `hook-agent` (Claude Code feeds it to the model), `hook-user` (hook stderr the human sees), `internal` (Go output captured and relayed by Python), `tmux`, `job-log`, `none` |
| `class` | REPORT, NO-REPORT, CONDITIONAL — the inventory. INFO and EXCLUDED — counted, not classified (below) |
| `kind` | validation, usage, not-found, state-guard, policy-guard, user-act, confirmation, gate, environment, fault, degraded, relay, idempotent, other |
| `remedy_or_decision_or_condition` | NO-REPORT: what the agent does instead. REPORT: the decision only the user can make. CONDITIONAL: `REPORT when …; NO-REPORT when …`. INFO/EXCLUDED: why |
| `notes` | `tension:` where the rule gives an answer that looks wrong, callers, side findings |

### Where the inventory lives

The row data is committed as `docs/research-2026-09-17-refusal-inventory.tsv`,
one row per site, because E-2159 consumes it mechanically while converting. It is
the only part of this research on disk: the findings themselves are this task's
outcome, and the design is E-2159's plan. When E-1531 ships the `task_content`
table the TSV is imported there and the file goes away — noted on E-1531.




