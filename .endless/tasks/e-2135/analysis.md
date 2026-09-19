Prompted by E-2132/E-2133 (2026-09-11): one omission filed as two tasks, then folded back by hand. Mike: 'EACH task requires a minimum amount of time for my scarcest resource, my attention. When you file two tasks *for purity* when you could have filed one you DOUBLE the amount of attention I have to SPEND.'

The hint is `_hint_same_session_root_cause` in src/endless/task_cmd.py, one of three emitted by `print_add_hints` (the others: recently-landed per --cleans-up target, and backlog pressure). It is fully DETERMINISTIC — one SQL query plus string formatting, no LLM, no `claude -p` — and wrapped in try/except so it can never fail the add. It arrived via E-1889's scope extension: 'the handoff line must also carry a "could this reasonably be done in the current session?" test and a "do these share a root cause?" test'.

== FOUR FAILURES, in increasing order of importance ==

1. FORM. It ends with a question — 'Does the one you just filed share a root cause with any of them?' A question of that shape invites an agent to DEFEND its reasoning rather than revise it, and is most inviting precisely when the agent has already deliberated and decided. It got answered in prose, twice.

2. DENIABILITY. The line already carries an imperative — 'File the cause, not each symptom' — and it still did not bind, because the agent did not believe it had filed a SYMPTOM; it believed it had filed two HALVES of one cause. 'Symptom' is a category that can be honestly disputed. A test with no category to dispute would be: state the single cause that covers both; if you can, it is one task. But note the agent PASSED that test in writing — it wrote the shared cause into the second task's own analysis — and filed anyway. Wording alone is therefore not the lever.

3. THE CANDIDATE SET IS WRONG (owner, 2026-09-12: 'the deterministic logic is flawed and needs to be revisited'). The query joins `live_tasks`, which is `SELECT * FROM tasks WHERE removed = 0` — it excludes REMOVED rows, not CLOSED ones. So obsolete, declined, confirmed, assumed and completed tasks all remain fold candidates. Observed live: after E-2133 was set obsolete and replaced_by E-2132, the very next `task add` in the session still offered E-2133 as a task to fold into. That is worse than noise — folding into shipped or abandoned work is exactly what ED-1550 rule 2 forbids ('NEVER reopen shipped work to extend it'), so the hint can push an agent into violating the rule it exists to enforce. At minimum the candidate set should be open tasks; whether it should be narrower still (e.g. only tasks this session filed AND that are pre-work) is part of this brainstorm.

4. TIMING AND COST. `print_add_hints` is called, per its own docstring, 'after the task and its relations exist'. Filing costs one command; folding afterwards costs rewriting the keeper's description, its title, a merge of both analyses, then `task replace` — four commands plus judgement. The gradient runs against ED-1550: the rule wants folding to be the default, and the tooling makes filing the default and folding a penalty paid on top of admitting error. A post-hoc hint is being asked to beat sunk cost.

== CANDIDATE INTERVENTIONS to weigh, not a plan ==

  a. Gate BEFORE creation. The second `task add` in a session refuses and requires a disposition: fold, or assert separateness by name (e.g. --separate-cause). Before anything exists, compliance is free, and the flag makes splitting a claim somebody made rather than a default nobody chose.
  b. Make folding one command. `endless task fold <this> --into <that>` appends description and analysis under a heading in the keeper and closes this one with the replaced_by relation. If the cheap path is the right path, wording stops having to carry the load.
  c. Fix the candidate set (point 3). Independently correct regardless of what else is chosen.
  d. Reword per point 2.
  e. Do nothing to the hint and accept the ratio.

(a) alone still leaves a four-command remedy. (b) alone makes the post-hoc hint actionable for the first time. Together, the gate's refusal can print the exact one-liner that satisfies it. Worth weighing against ED-1550 rule 7 ('closing is work: obsolete, decline and park are the only fast lever') — a cheap fold is arguably a fifth fast lever.

Owner asked for this as a brainstorm so the specifics are settled in session rather than pre-decided here.