# E-1064: Audit analysis fields on tasks for accuracy and staleness

Survey of the 41 tasks where `tasks.analysis IS NOT NULL` after E-1060 finalised. Goal: flag entries whose analysis content has drifted from current code/decision state, and recommend refresh-or-leave-as-is per item. No analysis content rewritten in this pass.

## Method

1. Pulled `(id, status, analysis)` for all 41 records.
2. Cross-referenced status with git log (`--grep "E-NNN"`) and existing plan files in `.endless/plans/`.
3. Bucketed each into one of:
   - **A. Shipped — analysis is historical record.** Status verify/confirmed/assumed/declined; analysis describes options or punch lists that the implementation has resolved. Low urgency. Trim only if it becomes a navigation hazard.
   - **B. Shipped/locked — analysis still presents a live decision.** Active mismatch between analysis content and current state of the world. Worth refreshing before someone reads the analysis and acts on stale options.
   - **C. Time-frozen data.** Analysis cites byte counts, file paths, or counts that were a snapshot of one moment. Refresh before acting on the task.
   - **D. Currently relevant.** Status needs_plan or in_progress; analysis informs the upcoming work.
   - **E. Obsolete/declined.** Analysis is archival; no action.

## Findings

### B. Shipped/locked but analysis still presents a live decision (refresh recommended)

| ID | Status | Why analysis is stale | Recommendation |
|----|--------|----------------------|----------------|
| E-958 | confirmed | "Pending decision (placeholder until resolved)" with open vocabulary questions, but `informs` was dropped (E-1003) and `documents` promoted (E-1007), and the relations CLI shipped via commit `e372759`. The frozen vocabulary differs from what the analysis enumerates. | Replace analysis with a one-line "vocabulary frozen — see E-1003 / E-1007" pointer, OR delete analysis entirely (description already conveys what the task was). |
| E-987 | ready | Analysis lists Options A-E and tags the task `--type research`, but the design was locked to Option F (auto-commit endless-managed files independently) per commit `1825a35` and `.endless/plans/E-987.md`. Anyone reading analysis would think the decision is still open. | Refresh analysis to "design locked to Option F (see E-987 plan file)", OR drop analysis since the plan file is canonical. |
| E-993 | obsolete | Status flipped to obsolete; analysis still reads "STATUS: UNRESOLVED. Needs discussion and explicit decision before implementation." Direct contradiction. | Drop analysis or prepend "Resolved: marked obsolete YYYY-MM-DD". |
| E-1037 | obsolete | Analysis ends with "Supersedes E-1037" — but that's the wrong direction; E-1038's analysis says it supersedes E-1037, and the task itself is now obsolete. Reader sees "supersedes itself", which is nonsense as written. | Drop analysis or replace with one-line "obsoleted by E-1038". |

### C. Time-frozen data (refresh before acting)

| ID | Status | Stale data | Recommendation |
|----|--------|-----------|----------------|
| E-979 | needs_plan | Byte counts for `e-967` worktree's events file (150651 vs 153226) snapshotted 2026-04-29 or so. May have moved on. | Re-measure when the task is picked up; don't trust cited bytes. |
| E-1034 | needs_plan | Specific line numbers in `cmd/endless-hook/claude.go` (lines 690, 713, 726, 732, 740) and plan files. Other commits may have shifted those line numbers. | Re-grep `sidecar` before editing. |
| E-994 | needs_plan | Cites `cmd/endless-hook/claude.go:538`. Drift likely after subsequent hook work (E-1011, E-1012, E-1027, E-1028, E-1033). | Re-locate `snapshotPlanFile` before editing. |

### A. Shipped — analysis is historical record (low urgency, leave unless aggregating)

These shipped or terminalised since the analysis was written. Their analysis still reads as "design exploration" or "punch list", which is now historical. No active misdirection — the status communicates done — but the field has stopped earning its keep. Consider trimming if/when the analysis field gains a CLI surface that displays it (today nothing reads it).

| ID | Status | Implemented by |
|----|--------|----------------|
| E-957 | assumed | `e372759` (general-purpose task link/unlink CLI) |
| E-977 | obsolete | folded into E-1006/E-1007 work |
| E-981 | confirmed | `acc7291` (--decision flag) |
| E-990 | verify | `d5f94f6` (session cd) |
| E-991 | verify | `0b0f1d0` (session show) |
| E-992 | verify | `0b0f1d0` (session history default) |
| E-1003 | confirmed | E-1002 cascading changes |
| E-1004 | verify | `a9ba29f` (cwd-aware project root) |
| E-1007 | confirmed | `3061a4d` (documents promoted) |
| E-1011 | verify | `5e12ac7` (companion backfill) |
| E-1012 | verify | `4f69a05` (block git commits) |
| E-1017 | confirmed | decision-record only; no implementation needed |
| E-1020 | confirmed | `e180710` (XDG support) |
| E-1027 | verify | `7219eb9` (companion worktree_path) |
| E-1028 | verify | `6e048ea` (hook regex E-prefix) |
| E-1033 | verify | `58ac6c8` (refresh on every UserPromptSubmit) |
| E-1038 | verify | `9ea22b3` (reduce session use activation) |

### D. Currently relevant (no change recommended)

Analysis informs ongoing or upcoming work. Leave as-is.

| ID | Status |
|----|--------|
| E-966 | needs_plan |
| E-984 | needs_plan |
| E-985 | needs_plan |
| E-986 | needs_plan |
| E-996 | needs_plan |
| E-998 | needs_plan |
| E-999 | needs_plan |
| E-1000 | needs_plan |
| E-1001 | needs_plan |
| E-1005 | needs_plan |
| E-1008 | needs_plan |
| E-1013 | needs_plan |
| E-1016 | needs_plan |
| E-1018 | needs_plan |
| E-1019 | needs_plan |
| E-1029 | in_progress |

### E. Obsolete/declined — archival, no action

| ID | Status |
|----|--------|
| E-767 | declined |

## Cross-cutting observation: analysis has no CLI surface

`tasks.analysis` was added to the schema and populated for these 41 records by some out-of-band mechanism (raw SQL or one-time migration; no `--analysis` flag exists on `endless task add` / `update`, and no Python or Go code in `src/` or `cmd/` reads it). Until the column is wired into the CLI, audit findings cannot be acted on through the normal event-sourced path without re-tripping the architectural concern raised in E-993 (raw-SQL writes bypass the event log).

Two directions worth a follow-up task:

1. **Add `--analysis` / `--analysis-file` flag** to `endless task add` and `endless task update`, with display in `endless task show`. Without this, the field is write-only-via-SQL and not actionable.
2. **Decide the field's role.** Is `analysis` long-form rationale (then it overlaps with `text`/plan files), or is it scratch space for design exploration before promotion to a plan file? E-999 is already the design task for "plan vs analysis content separation" and should absorb this question.

## Recommended follow-ups

- File one task to refresh-or-drop analysis on **E-958, E-987, E-993, E-1037** (bucket B). Concrete, contained, ~20 minutes of edits.
- File one task to wire `analysis` into the CLI (or fold this into E-999's design pass). Without it, the field cannot be acted on safely.
- Buckets A and C are not actionable on their own — flag them in the relevant tickets if/when those tickets are picked up.

No task additions made by this audit pass; the user requested a survey, not implementation.
