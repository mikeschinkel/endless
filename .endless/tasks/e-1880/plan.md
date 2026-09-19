# E-1880 — `endless task report` contradicts the handoff contract it serves

Observed live at the E-1870 handoff. Two defects, both in
`src/endless/report_cmd.py::_render_facts` (~L246-262), plus one supporting
change in the Go fact query.

## The defects

### 1. The report emits exactly what the handoff forbids

`_close.tmpl` tells every spawned session two things at once: "run `endless task
report <id>` and relay its output **verbatim**", and "Do NOT recap task
**status**, phase, or **relationships**". `_render_facts` unconditionally emits
`Task: E-NNNN`, `Status: <status>`, `Landed: yes`, and the relationship rows —
under a steer prompt that says "add nothing else". The session cannot satisfy
both instructions; on E-1870 it obeyed the tool and delivered the status recap
the user had explicitly asked not to receive.

### 2. The same IDs print twice under two labels

`successors` (the `cleans_up` relation) and `children` (the parent link) are
rendered as independent lines. A task filed with `--parent E-N --cleans-up E-N`
— the exact pattern every handoff instructs a session to use — lands in both.
E-1870's actual output listed E-1872..E-1875 twice, verbatim. This is not
misuse; it is the recommended usage.

## The decision

**The command holds itself to the same bar it already enforces on the agent.**

This is not a new policy — it is `report_cmd`'s own, applied consistently. The
`note-check` prompt already DROPs any agent note that "restates something already
visible in git/task state". `Status:`, `Landed:`, and the parent/child rows are
*precisely* that: computable from `task show` / `session status`. The command
currently exempts its own output from the rule it enforces on everyone else.
Closing that gap resolves both defects and needs no new judgement call.

Concretely, `_render_facts` emits a line only if the user could not already know
it:

| Line | Disposition | Why |
|---|---|---|
| `Task: E-NNNN` | **remove** | The user typed the ID. |
| `Status: <status>` | **remove** | `session status` renders it; the flip *is* the contract. |
| `Landed: yes` | **remove** | Computable from `task show`. |
| `Follow-ups you filed:` | **keep** | Tasks the session created that the user did not order — the one relationship they cannot know without querying. |
| `Children:` | **keep only when `type == epic`** | The epic handoff explicitly says "lead with the state of the children"; for every other type it is a recap. |
| Notes / questions / anomalies | **unchanged** | Already gated; already non-computable by construction. |

Dedupe rule: an ID rendered under `Follow-ups you filed:` is never repeated under
`Children:`. With `Children:` restricted to epics this collision is rare, but the
dedupe is unconditional so the two lists can never disagree again.

### Empty fact block

Removing three always-on lines makes the common clean handoff render an **empty**
fact block, and the current steer text ("Report the following… add nothing else")
then introduces nothing. Decision: `report_item` branches on an empty block and
emits a distinct steer — a new `steer-empty` prompt name in `report_prompts`
alongside `steer` — whose text tells the agent there is nothing to report beyond
its deliverable, and to say nothing rather than manufacture a summary. It is a
registered prompt name so it stays user-editable like the other three
(ED-1531 Req 5); unknown-name records remain ignored.

## Implementation

1. `internal/sessionquerycmd/session_query.go` — add `type` to the `task-report`
   JSON payload (currently `{task_id, status, landed, successors[], children[]}`).
   Update the usage line at L137 and `task_report_test.go`, which pins the
   contract.
2. `src/endless/report_cmd.py::_render_facts` — apply the table above; drop the
   three lines, gate `Children:` on `facts["type"] == "epic"`, subtract the
   successor IDs from the children list before rendering.
3. `src/endless/report_prompts.py` — add `STEER_EMPTY = "steer-empty"` to
   `_KNOWN` and `DEFAULTS`; `report_item` selects it when the fact block is empty.
4. `internal/templatecmd/templates/handoff/_close.tmpl` — no change needed. Once
   the command stops emitting status and relationships, "relay verbatim" and "do
   not recap status/relationships" are consistent, which is the point.

## Verification — `tests/tasks/e-1880-verify.sh`

1. **Fail-fast:** `uv run pytest tests/test_task_report.py` and
   `go test ./internal/sessionquerycmd/...`.
2. **No status recap:** render a report for a task in `unverified` and assert the
   output contains neither `Status:` nor `Landed:` nor `Task: E-`.
3. **Dedupe / the E-1870 case:** fixture a non-epic task with two children filed
   as `--parent N --cleans-up N`; assert each ID appears **exactly once** across
   the whole output, and that no `Children:` line is rendered.
4. **Epic carve-out:** same fixture with `type=epic`; assert `Children:` IS
   rendered, and still that no ID appears twice.
5. **Empty block:** a task with no follow-ups, notes, questions, or anomalies
   renders the `steer-empty` prompt and never the `steer` prompt.
6. **Contract consistency:** assert `_close.tmpl` still forbids recapping status
   and relationships, and that no fact-block line matches that prohibition — the
   regression guard that stops the two surfaces drifting apart again.
