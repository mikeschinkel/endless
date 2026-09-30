It is quite possible that the decision was misconstrued based on a preference for specific properties to write to the main config, specifically verbs, which ironically now get written to verbs.json instead of config.json.

## From the description

Outcome (2026-05-03): E-1112 is reversed by E-1140 — project-config writes (both verbs.json and config.json) revert to cwd-walk resolution. Auto-commit list inverts via E-1141 — verbs.json in, config.json out, with Go-side dedup at land.

Implementation tracked in E-1137 (path resolution change) and E-1138 (auto-commit + dedup). Meta-task E-1139 captures the need for typed decision-to-decision links so future revisits can express 'reverses' or 'modifies' as real relations rather than prose.

The notes added on this task today were written via raw SQL (analysis-style aside) and bypass the events log; that drift remains until E-999's analysis CLI lands or is otherwise reconciled.
