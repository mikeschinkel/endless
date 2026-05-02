Decision: standardize task relation vocabulary so the general-purpose linking CLI (E-957) can land without churn.

## Decided (2026-04-28; updated 2026-04-29)

**Verb:** `link` is the canonical command for creating typed relationships between tasks.

**Syntax:** `endless task link <source> --to <target> --as <type>` (and inverse `endless task unlink ...`).

**`block` is a shortcut, not a separate verb.** `endless task block <id> --by <other>` remains as a thin wrapper that calls `link --as blocked_by` under the hood. Same applies to other inverse-paired shortcuts (e.g. `replace`).

**Canonical dep_types (5 stored + inverse views):**
- `blocks` / `blocked_by`
- `implements` / `implemented_by` — link impl task to its decision
- `replaces` / `replaced_by`
- `documents` / `documented_by` — decision documents the rationale for a task (E-1007)
- `relates_to` — symmetric catch-all

**Storage convention:** active-voice. A row stores `source=actor, target=acted_upon, dep_type=<active_verb>`. So `dep_type='blocks'` reads "source blocks target". Inverses (`blocked_by`, `implemented_by`, `replaced_by`, `documented_by`) are query-time views — same row, source/target swapped.

**Schema:** `UNIQUE(source_type, source_id, target_type, target_id, dep_type)` — multiple typed relations between the same ordered pair are allowed (e.g. A blocks B AND A relates_to B can coexist).

**`documents` semantics:** "X documents Y" means X provides the record/rationale for Y. Used by `task add --decision <text>`, `task update --decision <text>` (auto-creates a decision-type task that documents the parent task), and `decision add --about <task>` (decision documents the task it's about). Distinct from `implements` (hard: task acts on decision) and `relates_to` (symmetric, generic).

**Removed from initial vocabulary (2026-04-29, E-1003):**
- `informs` / `informed_by` — verb is too vague to ever reach for. Per agent-reach principle: vocabulary that goes unused becomes noise.

**Promoted from deferred (2026-04-29, E-1007):**
- `documents` / `documented_by` — was deferred in the original lock for "no use case yet"; the use case arrived with E-980 (`--decision` flag) and `decision add --about`.

**Deferred (not adopted):**
- `decides` / `decided_by`: `implements` covers the hard case; nothing left for it.

## Why this matters

CLI flags hard-code these strings. Locking the vocabulary now prevents renaming churn once `task add --blocks <id>`, `task add --implements <id>`, etc. are wired up.

Schema is already permissive (CHECK constraints removed), so this is purely a UX/docs decision — no migration needed.

## Implements / refines this decision

- E-957 (general-purpose relation CLI) — implements this decision.
- E-1002/E-1003 (drop `informs`) — refines this decision.
- E-1006/E-1007 (promote `documents`) — refines this decision.
- E-977/E-978 (`implements` link type) — covered by the canonical set; obsoleted by E-957.
