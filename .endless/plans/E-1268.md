# Convert verbs.json to verbs.jsonl with union merge driver

## Problem
Pretty-printed JSON arrays end with `]` on a single line. When two branches
(main and a worktree) both append verb entries, git's line-based 3-way merge
sees competing edits to the same trailing region and conflicts even though
the operation is purely additive. The existing E-1141 dedup-and-bundle step
in `_dedup_worktree_verbs_against_main` mitigates this but does not fully
prevent the textual conflict at rebase time — demonstrated when landing
E-1264 against a main that had picked up new verbs concurrently.

## Solution
Switch the on-disk format from a JSON array to JSONL (one object per line),
and add a `merge=union` entry in `.gitattributes` so git auto-concatenates
both sides' new lines without manual resolution.

Duplicates produced by the union merge (same `value` added on both sides)
are silently absorbed by the existing dedup-on-load logic in
`_add_verb_to_file` (matchers.py:836–838).

## Design

### File format

Before:
```json
[
  {"value": "add", "definition": "to introduce..."},
  {"value": "fix", "definition": "to repair..."}
]
```

After (`.endless/verbs.jsonl` + machine `verbs.jsonl`):
```
{"value": "add", "definition": "to introduce..."}
{"value": "fix", "definition": "to repair..."}
```

### .gitattributes
```
.endless/verbs.jsonl merge=union
```

### Code changes
- `src/endless/matchers.py`:
  - `project_verbs_path()` / `machine_verbs_path()` → return `verbs.jsonl`.
  - `_load_verbs_list(path)` → read line-by-line, parse each as JSON.
  - `_save_verbs_list(path, verbs)` → write line-by-line.
  - Migration: at load time, if `verbs.json` exists at the same dir, read
    its array, merge into the JSONL, then delete `verbs.json`.
- `src/endless/worktree_cmd.py`:
  - `AUTO_COMMIT_GLOBS`: replace `.endless/verbs.json` with `.endless/verbs.jsonl`.
  - `_read_verbs_list` / `_dedup_worktree_verbs_against_main`: read/write JSONL.
  - Dedup logic remains valuable as a belt-and-suspenders measure (it still
    produces a clean bundle commit), but the union-merge fallback is now the
    primary safety net.

### Tests
- Update existing tests that assert `verbs.json` content to assert
  `verbs.jsonl`.
- Add migration test: pre-existing `verbs.json` is read and converted to
  `verbs.jsonl` on next load.
- Update `test_worktree_land_dedup.py` to use JSONL.

## Verification
- `endless verb list` continues to show all verbs after migration.
- `endless verb add <new>` writes a single line to `verbs.jsonl`.
- Concurrent verb adds on two branches followed by rebase auto-merges (no
  manual resolution).
- Existing `verbs.json` is migrated automatically on first load.
- `just test` passes.
