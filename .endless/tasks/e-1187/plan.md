# E-1187 — Soften and update stale worktree help strings; close out Layer F

> Re-scoped from the original "Layer F implementation" prompt. Layer F itself shipped in commit `d9fd547` (2026-05-06). What remains are two stale user-facing help blocks in `cmd/endless-hook/claude.go` and three task-status updates that bring the Endless board into alignment with the shipped code. Tone of the help strings is also being softened per Mike (2026-05-09).

## Context

The original prompt asked me to implement Layer F (auto-create task worktree on `task start`). Audit of decisions, tasks, and commits since E-971 was filed (2026-04-28) shows:

- **Layer F is shipped** in `d9fd547`. `task_cmd.py:1792-1823` calls `worktree_cmd.py:create_task_worktree`, which writes the companion at `.endless/worktrees/e-NNN/.endless/worktree.json`, is idempotent, and prints the cd-or-spawn hint.
- **E-1168 rescinded the plan-bearing-only gate** — every task gets a worktree on `task start`. The current implementation matches that decision (no plan-bearing check). Per Mike, this rescission still stands.
- **E-1186 aligned the directory convention** to `e-NNN` and removed the "no create verb" lock from E-971's plan.
- **E-1187's original trigger** — a live worktree at `.endless/worktrees/task-1031-session-focus` — no longer exists on disk. Search across `src/`, `cmd/`, `internal/` finds zero code paths producing the old `task-NNN-<slug>` directory convention; `worktree_cmd.py:548` builds `e-{task_id}` exclusively. **No recovery of the old worktree is needed for this work** — the audit goal is met by reading the current code, not by inspecting a stale on-disk artifact.

What the audit *did* find: two user-facing help blocks in `cmd/endless-hook/claude.go` carry pre-Layer-F instructions that mislead Claude/the user, plus tone that is harder than Mike wants ("not allowed" vs the actual norm of "highly discouraged when using endless").

This plan fixes those blocks and aligns the board.

## Changes

### File: `cmd/endless-hook/claude.go`

**Block 1 — direct-commit-to-main blocker (`claude.go:719-731`).** Three issues to address:

- Tone: "not allowed" → "highly discouraged when using endless" (Mike, 2026-05-09).
- Stale: line 722 `(or 'endless worktree land' once E-971 ships)` — `endless worktree land` shipped with the Layer D mutation slice; drop the conditional.
- Wrong convention: lines 724/728 example `e-XXX-<slug>` is the wrong branch shape. Layer F uses `task/NNN-<slug>` for the branch, `e-NNN` for the directory.
- Jargon: "surfaces as a deliberate violation" — Mike flagged this as unclear. Replace with plain language.

Proposed new text:

```
Direct commits to main are highly discouraged when using endless (E-1012).

main is the integration target. Make changes in a worktree on a per-task
branch, then merge via `endless worktree land <task-id>`.

If you have an Endless task for this work:
  endless task start E-NNN          # creates worktree at .endless/worktrees/e-NNN

Or by hand:
  git worktree add -b task/NNN-<slug> .endless/worktrees/e-NNN main
  cd .endless/worktrees/e-NNN
  # ... do work, commit ...
  endless worktree land E-NNN

Bypass (NOT recommended):
  git commit --no-verify
```

**Block 2 — Edit-in-main blocker (`claude.go:1177-1185`).** Two issues:

- Tone: "not allowed" → "highly discouraged when using endless"; "every edit goes through a worktree" → "every edit ideally should go through a worktree" (preserves modality per Mike's feedback memory on soft preferences).
- `endless pivot (when available)`: keep the reference. Per Mike (2026-05-09), `endless pivot` is intended (part of E-1031, currently paused) for in-session focus shifts; it is a real planned verb, not a stale reference. The `(when available)` modality already signals it's not yet shipped.

The branch/dir example on line 1184 (`task/NNN-<slug>` / `e-NNN`) is already correct — keep it.

Proposed new text:

```
Edits in main are highly discouraged when using endless (E-971).

main is the integration target — every edit ideally should go through a
worktree.

If you do not yet have an active task, create one and start it:
  endless task add "<title>"
  endless task start E-NNN          # auto-creates the worktree

If you already have an active task without a worktree:
  endless task start E-NNN          # idempotent; creates if missing

Or create the worktree by hand or via `endless pivot` (when available):
  git worktree add -b task/NNN-<slug> .endless/worktrees/e-NNN main
```

(The `redirectHint` appended on line 1185 is unchanged — it still produces the "Your active task E-N has a worktree at: <path>" suffix when applicable.)

### Endless board updates (status flips, no code)

After the help-string fixes land, three items need to come into alignment with shipped reality:

1. **E-1150** ("Add task-command worktree integration…"): description says "for plan-bearing tasks" — outdated per E-1168. Implementation is shipped via E-1170. Recommend: rewrite the description to match shipped reality ("…task start triggers worktree for every task per E-1168") and flip `in_progress → verify`.
2. **E-1168** (decision: "Every task gets a worktree, rescinds plan-bearing-only"): status is `needs_plan` but the decision was made and acted on. Flip to `confirmed` (decision is recorded; no plan needed for a rescission decision).
3. **E-1187** (this work): on land, flip `needs_plan → verify`.

## Cross-references (not in scope, but worth noting in the help text)

- **E-1213** ("Design adoption-friendly handling of user-produced dirty state in main"): `needs_plan`. Adds an `endless worktree move` verb to lift uncommitted changes from main into a fresh worktree. Once it ships, Block 1 could mention it as a third option ("If you already have uncommitted work in main: `endless worktree move`"). Out of scope here — keep this in mind for a follow-up edit when E-1213 lands.
- **E-1031** ("Add Session Focus entity for tracking session focus"): `in_progress` but on the back burner. Source of the `endless pivot` verb that Block 2 already references with `(when available)`. No edit needed.

## Out of scope

These E-971 children are separate work tracks and are NOT addressed here:

- E-1123 — branch-collision strategy for short-id session worktrees (session worktrees aren't implemented yet anyway).
- E-1142 — sync `sessions.active_task_id` on direct-CLI `task confirm`.
- E-1166 — better fallback for unset `origin/HEAD` in `_default_base_branch`.
- E-1190 — `endless worktree add` verb.
- E-1195 — review of PID-based lock-file ownership model.
- E-1203 — refuse `endless task start` when another live session owns the task (handled by another session, per Mike).

Session-worktree creation (the second worktree kind in E-971's design) is also not in scope; it's deferred and not part of E-971's v1 ship.

## Critical files

- `cmd/endless-hook/claude.go:719-731` — direct-commit blocker (Block 1).
- `cmd/endless-hook/claude.go:1177-1185` — Edit-in-main blocker (Block 2).

No tests are added: these are user-facing prose; correctness is verified by reading the strings and by exercising the blocked paths to confirm they print as expected.

## Verification

End-to-end smoke test in this worktree. Build the hook binary and trigger each blocker.

1. **Build the hook in this worktree** (per CLAUDE.md, `just install` is forbidden inside worktrees):
   ```
   just build
   ```
   Confirms the worktree's `bin/endless-hook` reflects the new strings. The `.claude/settings.json` override (Layer D) routes the worktree's session through this binary.

2. **Trigger Block 1 (direct commit to main)** by feeding a synthetic PreToolUse payload to the binary:
   ```
   echo '{"hook_event_name":"PreToolUse","tool_name":"Bash","cwd":"'"$HOME"'/Projects/endless","tool_input":{"command":"git commit -m test"}}' | \
     ./bin/endless-hook claude
   ```
   Expect: blockToolUse output containing the new "highly discouraged" tone, the `endless worktree land <task-id>` recommendation (no "once E-971 ships"), the corrected `task/NNN-<slug>` branch, and the simplified Bypass line.

3. **Trigger Block 2 (Edit in main)**:
   ```
   echo '{"hook_event_name":"PreToolUse","tool_name":"Edit","cwd":"'"$HOME"'/Projects/endless","tool_input":{"file_path":"'"$HOME"'/Projects/endless/README.md"}}' | \
     ./bin/endless-hook claude
   ```
   Expect: blockToolUse output with "highly discouraged" tone, "ideally should go through a worktree" modality, the `endless task start E-NNN` recommendation, and the preserved `endless pivot (when available)` reference.

4. **Confirm Layer F still works (regression check on the actual functionality):**
   ```
   endless task add "Smoke test E-1187 verification"   # returns E-NNN
   endless task start E-NNN
   ls .endless/worktrees/e-NNN/.endless/worktree.json  # exists
   endless worktree for-task E-NNN                     # prints the path
   endless task start E-NNN                            # idempotent: "already exists"
   endless worktree drop e-NNN --force                 # cleanup
   endless task update E-NNN --status obsolete
   ```
   Expect each step to succeed cleanly.

5. **Land via the standard flow**: `endless worktree land E-1187` from the worktree once everything's verified. Refresh the toolchain from main with `just install` after landing.

## Ship sequence

1. Make the two `claude.go` edits in this worktree (E-1187).
2. `just build`; run the verification steps above.
3. `endless task update E-1187 --status verify` once verified.
4. Wait for Mike to confirm; he flips to completed.
5. Land via `endless worktree land E-1187`.
6. From main: rewrite E-1150's description to match shipped reality, then `endless task update E-1150 --status verify`. `endless task update E-1168 --status confirmed`.
