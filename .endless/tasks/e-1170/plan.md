# E-971 Layer F — Auto-create task worktree on `task start`

## Context

Per E-971, every Claude session that edits code lives in a worktree. **Per-session worktrees** (Layer D, shipped in `7199f5f`) auto-create on SessionStart. **Per-task worktrees** persist across sessions and are adoptable; they should auto-create when a task transitions to `in_progress`. Layer F closes that gap so the per-task lifecycle is symmetric with the per-session lifecycle: `endless task start <id>` becomes the moment of birth for a task worktree, mirroring SessionStart for session worktrees.

Today `endless task start <id>` only flips status and emits `task.status_changed`. After Layer F: it also creates `.endless/worktrees/e-<id>/` on branch `task/<id>-<slug>` from main, writes the companion JSON, and prints a cd-or-spawn hint. Idempotent on re-run.

## Filed before plan approval

These decisions and follow-ups are recorded in the database, not just here:

| ID | Type | What |
|----|------|------|
| **E-1168** | decision | Every task gets a worktree (rescinds the original E-971 "plan-bearing only" rule) |
| **E-1169** | decision | Refuse `task start` with actionable recommendations when the plan file is uncommitted in main |
| **E-1167** | decision | Title-rename drift accepted: branch slug stays as the original after a title change |
| **E-1165** | task (later) | Port `endless task start` to Go (E-894 follow-up) |
| **E-1166** | task (later) | Fix `origin/HEAD` unset fallback in worktree base-branch detection |

## Decisions locked in this plan

1. **Every task gets a worktree** (E-1168). No plan-bearing gate. The brief's plan-bearing condition is dropped per Mike's call this session.
2. **Slug source = `tasks.title`**. Requires adding `title` to the existing SELECT in `start_item` — a one-column extension to a query already reading from `tasks`. Net SQL delta: +1 column. The full Python→Go port is filed separately (E-1165) so we don't bundle it here.
3. **Worktree directory** = `<project_root>/.endless/worktrees/e-<id>` (lowercase task id, no slug). Matches recent convention seen in `git worktree list` output.
4. **Branch** = `task/<id>-<slug>`. Matches recent convention.
5. **Slug rules** = E-971 spec (`.endless/plans/E-971.md:169`): lowercase, drop filler words `{a, an, the, to, from, of, for, with, in, on, at, by, and, or}`, replace non-alnum with `-`, collapse repeats, truncate to 40 chars at last `-` boundary. If empty, fall back to `"task"` (E-1167 covers the rename-drift edge).
6. **Companion JSON** = `{kind: "task", task_id: "E-<id>", base_branch: <branch>, branch: <branch>, created_at: <ISO UTC>}`. The `kind` field is forward-looking for Layer D's `WorktreeCompanion` round-trip (`internal/monitor/worktree_lock.go:34`).
7. **Base branch** = `git symbolic-ref --quiet refs/remotes/origin/HEAD` with `"main"` fallback. Limitation tracked in E-1166.
8. **Plan-file ride-along** (E-1169): if `.endless/plans/E-<id>.md` exists in main but is uncommitted, refuse `task start` and print recommendations. The recommended commands are listed in the implementation section.
9. **No `O_EXCL`** on the companion write. `git worktree add` is exclusive on its target path and branch; by the time we write the companion, only this process owns the directory.
10. **No CLI opt-out flag**. If users want bare-start behavior, that's a separate proposal.
11. **cwd-agnostic**. Worktree is created regardless of cwd; the user gets a hint and decides whether to switch.

## Implementation

### Critical files

- `src/endless/worktree_cmd.py` — add slug helper, base-branch helper, plan-file commit-check helper, and `create_task_worktree()` entry point.
- `src/endless/task_cmd.py` — extend `start_item` to call the new helper and add the `title` column to its SELECT.
- `tests/test_worktree_create.py` (new) — slug + base-branch unit tests.
- `tests/test_task_start_worktree.py` (new) — integration test of `start_item` worktree creation.

### Step 1: helpers in `src/endless/worktree_cmd.py`

Append after `_normalize_task_id` (line 391). Add `from datetime import datetime, timezone` to the imports at the top.

```python
_FILLER_WORDS = frozenset({
    "a", "an", "the", "to", "from", "of", "for", "with",
    "in", "on", "at", "by", "and", "or",
})


def _slugify_title(title: str) -> str:
    """Slug per E-971 spec for task branch names.

    Lowercase, drop filler words, replace non-alnum with '-', collapse
    repeats, truncate to 40 chars at a word boundary. Returns 'task' if
    the title contains only filler/punctuation.
    """
    cleaned = re.sub(r"[^a-z0-9]+", " ", title.lower())
    words = [w for w in cleaned.split() if w and w not in _FILLER_WORDS]
    slug = "-".join(words)
    if len(slug) > 40:
        truncated = slug[:40]
        if "-" in truncated:
            truncated = truncated.rsplit("-", 1)[0]
        slug = truncated
    return slug or "task"


def _default_base_branch(project_root: Path) -> str:
    """Best-effort default-branch detection. Falls back to 'main'.

    Limitation tracked in E-1166: origin/HEAD may be unset on fresh
    clones, leaving us with the literal 'main' fallback even when the
    repo's actual default is master/develop.
    """
    try:
        ref = _git(["symbolic-ref", "--quiet", "refs/remotes/origin/HEAD"], cwd=project_root)
    except subprocess.CalledProcessError:
        return "main"
    return ref.removeprefix("refs/remotes/origin/") or "main"


def _check_plan_file_committed(task_id: int, project_root: Path) -> str | None:
    """If .endless/plans/E-<id>.md exists but is dirty/untracked in main,
    return an error message with recommended commands. Otherwise None.

    The plan file lives in main's working tree but won't propagate to a
    new worktree (git worktree add starts from a commit, not the index).
    Per E-1169, refuse with recommendations rather than auto-commit.
    """
    plan_rel = f".endless/plans/E-{task_id}.md"
    plan_abs = project_root / plan_rel
    if not plan_abs.exists():
        return None
    res = _git_run(["status", "--porcelain", "--", plan_rel], cwd=project_root, check=False)
    if res.returncode != 0 or not res.stdout.strip():
        return None  # clean (committed) or git error we won't block on
    return (
        f"Plan file {plan_rel} is uncommitted in main; it will not "
        f"appear in the new worktree.\n\n"
        f"Capture it before starting the task. Recommended:\n"
        f"  git -C {project_root} add {plan_rel}\n"
        f"  git -C {project_root} commit -m 'Add plan for E-{task_id}'\n"
        f"\nThen retry: endless task start E-{task_id}"
    )


def create_task_worktree(task_id: int, title: str, project_root: Path) -> tuple[Path, bool]:
    """Create the per-task worktree.

    Returns (worktree_path, created). 'created' is False if the worktree
    already existed (idempotent no-op).
    """
    canonical = f"E-{task_id}"
    slug = _slugify_title(title)
    branch = f"task/{task_id}-{slug}"
    wt_dir = project_root / ".endless" / "worktrees" / f"e-{task_id}"
    base = _default_base_branch(project_root)

    if wt_dir.exists():
        existing = _read_companion(wt_dir)
        if existing and existing.get("task_id") == canonical:
            return wt_dir, False
        raise click.ClickException(
            f"Path {wt_dir} exists but does not belong to {canonical}. "
            f"Resolve manually before retrying."
        )

    msg = _check_plan_file_committed(task_id, project_root)
    if msg:
        raise click.ClickException(msg)

    wt_dir.parent.mkdir(parents=True, exist_ok=True)
    try:
        _git_run(["worktree", "add", "-b", branch, str(wt_dir), base], cwd=project_root)
    except subprocess.CalledProcessError as e:
        raise click.ClickException(
            f"git worktree add failed for {canonical}:\n{e.stderr or e}"
        )

    companion_dir = wt_dir / ".endless"
    companion_dir.mkdir(parents=True, exist_ok=True)
    companion = {
        "kind": "task",
        "task_id": canonical,
        "base_branch": base,
        "branch": branch,
        "created_at": datetime.now(timezone.utc).isoformat(),
    }
    (companion_dir / "worktree.json").write_text(
        json.dumps(companion, indent=2) + "\n"
    )
    return wt_dir, True
```

### Step 2: extend `start_item` in `src/endless/task_cmd.py:1639-1666`

Two surgical changes:

1. Line 1644 — add `title` to the SELECT:
   ```python
   "SELECT id, title, description, status FROM tasks WHERE id = ?"
   ```

2. After line 1666 (the existing `click.echo("• Started: ...")`), append the worktree-creation block:

   ```python
   from endless.worktree_cmd import create_task_worktree, _project_root

   try:
       project_root = _project_root()
   except click.ClickException:
       return  # No project root resolvable; existing behavior preserved.

   slug_source = row[0]["title"] or row[0]["description"] or "task"
   wt_path, created = create_task_worktree(item_id, slug_source, project_root)

   if created:
       click.echo(
           click.style("•", fg="cyan")
           + f" Created task worktree: {wt_path}"
       )
       click.echo(f"  Switch sessions to it: cd {wt_path}")
       click.echo(f"  Or spawn a fresh one:    endless task spawn E-{item_id}")
   else:
       click.echo(
           click.style("•", fg="cyan")
           + f" Worktree already exists: {wt_path}"
       )
   ```

The lazy import dodges any circular-import risk since `worktree_cmd` already imports from `task_cmd`.

### Step 3: CLI binding

No change to `cli.py:813-818`.

## Reuse map

- `_git_run`, `_git`, `_read_companion`, `_project_root`, `COMPANION_FILENAME` — already in `worktree_cmd.py` (lines 42, 58, 68, 119, 366).
- `_normalize_task_id` — already in `worktree_cmd.py:386` (we don't need it directly here but the canonical-ID convention matches).
- Hint formatting (`click.style("•", fg="cyan")`) — established in both files.
- No new dependencies; only stdlib `datetime`, `re`, `json`, `pathlib`.

## Verification

### Unit tests (new file `tests/test_worktree_create.py`)

- `_slugify_title`:
  - `"Move title verbs from hardcoded list to database table"` → `"move-title-verbs-hardcoded-list-database"`
  - `"The to from"` → `"task"` (all-filler fallback)
  - `"Edit user's profile (UI/UX)"` → `"edit-user-s-profile-ui-ux"`
  - 50-char input → ≤40 chars, ends at a `-` boundary
  - `"   "` (whitespace only) → `"task"`
- `_default_base_branch`: monkey-patch `_git` to return `"refs/remotes/origin/develop"` → `"develop"`; raise `CalledProcessError` → `"main"`.
- `_check_plan_file_committed`: temp git repo; plan file absent → `None`; plan file untracked → message with recommended commands; plan file modified-but-tracked → message; plan file committed-clean → `None`.

### Integration test (new file `tests/test_task_start_worktree.py`)

Fixture: tmp dir, `git init`, initial commit on `main`, register endless project, insert a task, optionally `_write_task_plan_file`. Then run `start_item`:

1. **Happy path (no plan file)**: bare task, no worktree exists.
   - `.endless/worktrees/e-<id>/` exists.
   - `git branch` lists `task/<id>-<slug>`.
   - Companion JSON parses with `kind == "task"`, `task_id == "E-<id>"`, `base_branch`, `branch`, `created_at`.
   - Output includes "Created task worktree:" and the cd hint.
2. **Happy path (committed plan file)**: plan file exists and is committed → worktree creation succeeds, plan file present in the worktree.
3. **Refusal path (uncommitted plan file)**: plan file exists, untracked → `ClickException` with the recommended-commands message; no worktree created.
4. **Idempotent re-run**: `start_item` again on same id → no error, no duplicate, prints "Worktree already exists".
5. **All-filler title**: title `"The to from"` → branch `task/<id>-task`.
6. **Discoverability**: `endless worktree list` classifies the new worktree `active`; `endless worktree for-task E-<id>` returns the path.
7. **Land round-trip**: `endless worktree land <id>` end-to-end on a worktree with one commit succeeds (verifies companion shape).

### Manual smoke

```sh
just build
PYTHONPATH=$(pwd)/src python -m endless task start <some-id>
git worktree list
cat .endless/worktrees/e-<id>/.endless/worktree.json
PYTHONPATH=$(pwd)/src python -m endless worktree list
PYTHONPATH=$(pwd)/src python -m endless worktree for-task E-<id>
```

Then verify the same task started a second time prints "Worktree already exists" without error.

## Out of scope (don't touch)

- SessionStart, UserPromptSubmit, PreToolUse hooks (Layers D and E shipped).
- `endless worktree land` and `drop` (E-987 / E-1056 shipped).
- E-1138 (verbs.json migration in land's auto-commit list).
- E-1165 (Python→Go port of `start_item`) — filed; do later.
- Auto-cd or auto-spawn the session into the new worktree.
- Branch-rename when a task title changes after first start (E-1167 documents the accepted drift).

## Workflow

1. Set E-971 child task for Layer F to `in_progress`. (If no specific child exists, create one or update E-971 itself.)
2. Implement in this worktree.
3. `just build` and run tests.
4. Manual smoke.
5. Set status `verify`.
6. `endless worktree land <task-id>` to ship.
