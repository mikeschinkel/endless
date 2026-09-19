# Agreed data model (from E-1771 co-design)

Design input for this task's schema, settled while building E-1771. E-1771
computes these facts and renders them into a steering prompt but does NOT
persist; this task writes them as queryable rows.

## Proposed tables

```
report            (id, task_id FK, session_id, status, created_at)
report_task_ref   (report_id FK, task_id FK, role[successor|child], relation?, status_at_report)
report_note       (report_id FK, kind[anomaly|discovery], text, gate_verdict)
report_question   (report_id FK, text, answer?, type[text|integer|real|boolean|choice], style?, gate_verdict)
```

- Related tasks are an **FK to `tasks`**, not duplicated detail. Snapshot only
  the fields whose historical value you'd query — **status_at_report** (and
  likely **phase**) — and live-join the stable detail (title, type, relations).
- **Never capture transient render/dirty state.** Git/worktree anomalies are an
  agent concern surfaced in the prompt; they are neither user-facing nor
  analytic, so they get no table (decided in E-1771).
- Questions: `type` is the data type; `style` is a presentation hint
  (`yes-no`/`true-false` for boolean, `single`/`multiple` for choice) — kept
  distinct because a true/false question is not necessarily yes/no.
- Explicit-about-absence applies HERE (an empty successor set is a recorded
  fact), unlike the steering prompt which stays silent on empty categories.
- Store **normalized relational rows**, never one opaque blob — the whole point
  of capture is that tooling can query individual facts. The scalar-vs-collection
  rule that shaped these tables (scalars are columns, collections are child
  rows) maps 1:1 from the fact struct E-1771 already builds; no serialization
  step is needed — E-1771 reads facts into the struct, this task writes the
  struct to rows.
