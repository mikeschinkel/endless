# Post-land residue check — PLAN

After a land, verify that any paths the land newly un-ignored were left with **no untracked
residue** on main. If a post-land script was absent or had a blindspot and cruft remains,
report it loudly. Verifies the *outcome* rather than trusting that a script exists.

## Motivation
Removing a `.gitignore` entry leaves the formerly-ignored files on disk as **untracked**
(a commit only moves tracked content, so no land deletes them). E-1799's post-land script
is meant to clean them, but an agent's script can miss some. This step tests the end state
so a blindspot — or a missing script — surfaces loudly instead of leaving cruft that a
*future* land could sweep into a commit.

## Design: allow-then-verify (not a gate)
The land proceeds normally — no pre-merge refusal. (At pre-merge the paths are still ignored
in main and can't be swept; the real risk is a *later* land sweeping leftover cruft, which a
loud post-land report gets cleaned before then.) The check runs after the merge and after
the post-land script, and reports.

## 1. Placement
Two touch-points in `land_worktree`, both on the success path, not reached by the
`record_only` early return:
- **Pre-merge snapshot** (cheap, pure data — not a gate): near Step 1, capture
  `I = git -C <main> ls-files --others --ignored --exclude-standard` — the set of files that
  are ignored-and-present in main under the *current* (pre-land) rules.
- **Post-script check**: a distinct new step immediately **after** the post-land-script step
  (which is after `_record_landing`) and before the best-effort worktree sweep.
This is a distinct step, not a change to `_git_status_partition` (that runs pre-merge where
the paths are still ignored and invisible).

## 2. Residue computation (compare git's own classifications; never read `.gitignore`)
- Post-script, compute `U = git -C <main> ls-files --others --exclude-standard` — untracked,
  not-ignored, present files in main under the *now-merged* (post-land) rules.
- **Residue = I ∩ U.** A path in both was ignored-and-present before and is
  untracked-and-present after → it was un-ignored by this land and not removed. Tracked
  content drops out (not in `U`); script-removed files drop out (not present); files this
  land didn't un-ignore drop out (not in `I` as *newly* un-ignored — they were ignored
  before and, if still ignored, are absent from `U`).

## 3. Reporting
- Residue empty → silent success (also the common no-op: a land that un-ignored nothing
  yields an empty intersection and does no work).
- Residue non-empty → loud, actionable error listing the residual paths, noting the
  post-land script that should have removed them (or that none was shipped) and the
  remediation (add/extend `.endless/hooks/post-land/e-<task>.sh`, or remove the files).
  **Non-fatal to the merge** (land already succeeded) but **exits non-zero**, mirroring
  land's existing "main advanced, follow-up step failed" surfacing so automation notices.

## 4. No bypass
Intentionally-tracked content under an un-ignored path is tracked → never in `U` → passes
silently. Only genuine untracked residue flags, which is correct. (A committed allow-list
could suppress a rare deliberately-kept-untracked false positive later; out of scope now.)

## 5. Verification
Single `tests/tasks/e-1800-verify.sh` on the established convention (shape per
`tests/tasks/e-1577-verify.sh`: `set -u`; `section`/`report_pass`/`report_fail`/`summary`;
`cd` to `git rev-parse --show-toplevel`; `endless() { uv run endless "$@" --db sandbox; }`;
exit 0 all-pass / 1 on any failure). Hand off exactly `esu && ./tests/tasks/e-1800-verify.sh`.
Cases (each drives a real land in the sandbox):
1. Un-ignore a path with on-disk cruft + a post-land script that removes it → no residue,
   silent pass.
2. Same, but the script removes only some → residue = the leftovers; non-zero exit; land
   still recorded.
3. Same, but NO post-land script → residue = all the cruft.
4. Un-ignore a path whose content is committed (tracked) → no residue → silent pass.
5. Land that un-ignores nothing → no-op.
Plus unit coverage in a new `test_worktree_land_post_land_residue.py` (mock the two
`ls-files` outputs; assert the I∩U computation, the report, the non-zero exit, and the step
placement).

## Relationship
Sequences on top of E-1799 (runs after its post-land-script step) — fine, E-1799 has landed.
Independent of the durable marker (E-1798).
