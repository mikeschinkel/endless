The legacy name task_deps originated when both endpoints were always tasks; in the new model the table describes task-sourced relations to any kind, so the name should follow.

Migration is metadata-only since all decision-sourced rows already moved to decision_relations as part of E-1378.

Quiet-window required (no parallel sessions; same constraint as E-1032/E-1261 ID migrations).
