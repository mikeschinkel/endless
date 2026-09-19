# E-1417 — Report rebase-conflict causes accurately in `worktree land`

## Context

`endless worktree land` rebases a task branch in two places, and BOTH miscategorize
a conflict — sending the user to the wrong step with the wrong fix:

- **Step 3.7** — `_drop_orphan_amendable_commits` (runs only when the branch has
  contiguous auto-amend orphan commits at its base). Its `rebase --onto base
  last_orphan` both drops the orphans AND replays the user's commits; a conflict in
  the replay raises `orphan auto-amend cleanup failed: <stderr blob>`, which names
  the wrong step (the drop succeeded — the replay conflicted) and buries the
  conflicting filenames in stderr.
- **Step 4** — the main `git rebase base` (runs when there are no base orphans). A
  conflict raises `rebase … failed. Likely cause: … an endless-managed auto-file
  (E-972) … git checkout main -- <globs>`. This is a **confident misattribution**:
  a real source-file conflict gets the auto-file story and a recovery that cannot
  work.

Observed repro: the E-1750 land hit Step 4 with a conflict in
`internal/sessionstatuscmd/session_status.go`; the message told the user to
`git checkout main -- .endless/*.jsonl`, which was irrelevant, turning a
copy-paste recovery into a debugging session.

Both messages also cite internal task IDs (E-972, E-1019) in user-facing text,
which the house rule forbids.

## Goal

On a rebase conflict in either step, report the **facts** confidently — which step,
which files — and present the plausible recoveries as **candidates to judge
between**, explicitly flagged as possibly-wrong, never a single confident
prescription. `land` stays non-interactive and stateless (it still aborts the
rebase). Decided with Mike 2026-07-10: approach (a) detect-and-present-candidates,
covering BOTH Step 3.7 and Step 4 via one shared helper (families b/c rejected —
see this task's analysis).

## Implementation

### 1. Shared conflict-message builder
Add to `src/endless/worktree_cmd.py`:

    def _rebase_conflict_message(worktree_path, base_branch, *, phase) -> str

Called from BOTH handlers while the rebase is still in progress (before any
`git rebase --abort`). It:
- Reads the unmerged paths: `git -C <wt> diff --name-only --diff-filter=U`.
- Optionally names the commit that failed to replay (`git -C <wt> rev-parse
  --short REBASE_HEAD` + its subject) so the user knows which of their commits
  hit the conflict.
- Classifies each conflicting path as auto-file (via the existing
  `AUTO_COMMIT_GLOBS` / `DB_LEDGER_DIR` matcher near `worktree_cmd.py:570`) vs
  source.
- Builds the message per §3.

### 2. Wire both call sites
- Step 3.7 handler (the `except subprocess.CalledProcessError` around
  `_drop_orphan_amendable_commits`): build the message with
  `phase="replaying your commits after dropping base auto-amend commits"`, THEN
  `git rebase --abort`, then `raise click.ClickException(msg)`.
- Step 4 handler (the `except` around `git rebase base_branch`): same, with
  `phase="rebasing your branch onto <base>"`. Delete the hardcoded auto-file
  cause and the E-972/E-1019 citations.

### 3. Message contract (this is the acceptance spec)
- Names the real step (the `phase` text), never "orphan cleanup" for a replay
  conflict.
- Lists each conflicting file on its own physical line.
- Every copyable git command on ONE physical line. No emojis. **No `E-NNN`
  internal task IDs.**
- **Auto-file-only** (every conflicting path is an auto-file): high confidence →
  the auto-file recovery (`git -C <wt> checkout <base> -- <globs>`, then retry).
- **Source conflict** (any conflicting path is a source file): a header that the
  cause is uncertain — e.g. "Likely causes (inspect and choose; the wrong
  recovery can duplicate or lose work):" — followed by ≥2 candidates:
  1. *main advanced with overlapping edits*: `cd <wt>` ; `git rebase <base>` ;
     resolve ; `git rebase --continue` ; retry land.
  2. *the branch re-introduces already-landed content* (e.g. an amended
     already-landed commit): capture the delta, `git reset --hard <base>`,
     re-apply only the delta, commit ; retry.
  Plus inspect lines: `git -C <wt> log <base>..HEAD` and
  `git -C <wt> diff <base>...HEAD`.
- Always aborts the rebase after building the message.

### Files
- `src/endless/worktree_cmd.py` — add `_rebase_conflict_message`; rewrite the two
  `except subprocess.CalledProcessError` handlers (Step 3.7 and Step 4).

## Tests
- New `tests/test_worktree_land_conflict_msg.py` — unit-tests `_rebase_conflict_message`
  against crafted tmp git repos (reuse the real-git-repo pattern from
  `tests/test_worktree_land_orphan_drop.py`): a **source** conflict → message names
  the file(s) + ≥2 candidate recoveries + no auto-file-only prescription; an
  **auto-file-only** conflict → the auto-file recovery; the two `phase` strings
  produce distinct step naming; output contains no `E-` token.
- Existing `test_worktree_land_orphan_drop.py` stays green.

## Verify
`tests/tasks/e-1417-verify.sh` — handoff is exactly:

    esu && ./tests/tasks/e-1417-verify.sh

It drives `_rebase_conflict_message` headlessly against crafted git repos (no full
land machinery), asserting the §3 contract for the source-conflict and
auto-file-only cases and the no-`E-NNN` rule. It is RED until this task is
implemented (helper absent → reported as a clear failure, not a crash) and GREEN
after.

## Coordination / non-goals
- E-1308 is the PREVENTIVE gate (refuse an already-landed re-land before the
  rebase); this task is the DIAGNOSIS after a conflict. Complementary — the
  "already-landed" candidate here mirrors E-1308's recovery.
- E-1416 (sibling: uncommitted-artifact land failure) — share this message style
  if it lands first.
- Out of scope: auto-resolving conflicts; making `land` interactive/stateful
  (rules out families b/c); changing orphan-detection logic.
