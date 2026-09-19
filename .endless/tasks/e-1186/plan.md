# Implement E-1186 — remove "no create verb" lock and align dir convention in E-971

## Context

E-971's "Decisions locked" list contains a hard prohibition (`No \`create\` verb`) that was misderived from a soft preference, plus a directory-naming convention (`<id>-<slug>`) that no longer matches reality (CLAUDE.md, on-disk worktrees, and E-1150's description all use `e-NNN`). E-1186 corrects E-971 to match what's actually true and to make room for `endless worktree add` (E-1190) — an explicit verb covering verify worktrees and other gaps the auto-creation triggers don't fill.

This task is text-only: edit `.endless/plans/E-971.md`, capture a separate decision entity in the task DB, commit on the `task/1186-remove-no-create-verb` branch, mark E-1186 verify, return Mike's tmux focus. No code paths change here — E-1187 handles `<id>-<slug>` references in code.

## Critical files

- `.endless/plans/E-971.md` — the only file edited.
- `.endless/plans/E-1186.md` — canonical plan; **do not modify** during implementation (per E-1199/E-1200).

## Edits to `.endless/plans/E-971.md`

Three logical edits, applied via `Edit` tool (each scoped narrowly to keep the diff easy to review):

### Edit 1 — remove "No `create` verb" decision (line 19)

Delete the entire bullet:

```
- **No `create` verb.** Worktree creation is always a side-effect of session start (per-session) or plan-bearing task start (dedicated).
```

Keep "No `switch` verb" (line 20) intact — that's a real technical constraint, not a misderived preference.

Do **not** add a softer replacement bullet. Per the E-1186 plan, this was considered and rejected as ceremony.

### Edit 2 — align dir convention in "Layout inside the repo" (line 13) and "Task worktree" trigger (lines 69–73)

**Line 13** currently reads:

```
- **Layout inside the repo**: `.endless/worktrees/<name>/`, gitignored (E-975 confirmed; E-976 implemented in commit `32c4b75`).
```

Replace with:

```
- **Layout inside the repo**: `.endless/worktrees/e-NNN/` canonical, `.endless/worktrees/e-NNN-<differentiator>/` for variants (e.g., `e-1130-verify`); gitignored (E-975 confirmed; E-976 implemented in commit `32c4b75`).
```

**Lines 69–73** ("Task worktree" subsection) — branch convention stays slug-bearing; only the dir name drops the slug. Update step 1 and step 3:

Before:
```
1. Compute branch name `task/<id>-<slug>`.
...
3. Otherwise: `git worktree add -b <branch> .endless/worktrees/<id>-<slug> main`, write companion with `kind='task'`, `task_id`.
```

After:
```
1. Compute branch name `task/<id>-<slug>` (canonical) or `task/<id>-<differentiator>` (variants — e.g., `task/1130-verify`).
...
3. Otherwise: `git worktree add -b <branch> .endless/worktrees/e-<id> main`, write companion with `kind='task'`, `task_id`.
```

After this edit, no `<id>-<slug>` token remains anywhere as a *path* (it stays in *branch* names by design).

### Edit 3 — add "Explicit (`endless worktree add`)" subsection after the Task worktree subsection (after line 73)

Insert a new subsection at the end of "Worktree creation triggers", after the Task worktree numbered list ends (line 73):

```
**Explicit (`endless worktree add`).** For cases neither auto-trigger covers — primarily verify worktrees for manual re-testing of completed work. See E-1190.
```

## Decision capture

Run after the edits land but before the commit (the decision is independent of the diff):

```sh
endless task add --type decision \
  "Removed 'No create verb' lock from E-971; verify use case discovered via dogfooding" \
  --description "$(cat <<'EOF'
The original lock was misderived from a soft preference. The verify-worktree workflow does not fit either of E-971's auto-creation triggers. An explicit `worktree add` verb covers the gap without contradicting the design's intent (most worktree creation still happens as a side-effect; the explicit verb is the escape hatch).
EOF
)"
# Capture the returned ID literally — do not guess.
endless task link <decision-id> --to E-971 --type documents
```

Use the literal ID returned from `task add` (per the "Use returned task ID literally" memory — task IDs advance globally across parallel sessions).

## Commit

Single commit on `task/1186-remove-no-create-verb`, file edit only:

```sh
git add .endless/plans/E-971.md
git commit -m "$(cat <<'EOF'
Update E-971 plan: remove 'no create verb' lock and align dir convention (E-1186)

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

The decision entity is a DB op, not a tracked file — don't try to stage it.

## Verification

All four checks must pass after edits land. Run from the worktree root:

```sh
# 1. The removed lock is gone:
grep -n "No \`create\` verb" .endless/plans/E-971.md   # → no match

# 2. No leftover <id>-<slug> path tokens:
grep -n "<id>-<slug>" .endless/plans/E-971.md          # → no match

# 3. The new explicit-creation trigger is documented:
grep -n "endless worktree add" .endless/plans/E-971.md # → at least one match

# 4. The decision entity exists and links to E-971:
endless task detail <decision-id>                       # → shows --documents E-971 link
endless task list --type decision | grep "Removed 'No create verb'"
```

Spot-check that "No `switch` verb" (line 20) is still present and unchanged — Edit 1 must not collateral-damage it.

## Attach this implementation plan to E-1186

Before flipping status, attach this plan file to the E-1186 task's text field so the task carries its own implementation breakdown alongside the canonical design plan at `.endless/plans/E-1186.md`:

```sh
endless task update E-1186 --text /Users/mikeschinkel/.claude/plans/implement-e-1186-you-atomic-journal.md
```

The canonical plan at `.endless/plans/E-1186.md` is **not** modified; it stays frozen per E-1199/E-1200. This is a separate artifact (the implementation breakdown) attached to the task DB record.

## Status flip + tmux return

```sh
endless task update E-1186 --status verify
tmux switch-client -t %93
```

## Out of scope (do not do these here)

- Touching code paths or other plan files for `<id>-<slug>` references — E-1187.
- Implementing E-1190 (`endless worktree add` verb) — only documenting that it's coming.
- Adding a softer replacement for the removed `No create verb` lock — explicitly rejected.
- Modifying `.endless/plans/E-1186.md` — canonical plan, frozen during implementation.
- Touching the `No \`switch\` verb` decision.
- Cleaning up the unrelated `.endless/db-ledger/` / deleted `events/*.jsonl` state visible in `git status` — that's E-1197 fallout, not this task's concern.
