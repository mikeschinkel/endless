# E-1144 — Rename spawn-created tmux windows to `<project>_<slug>[E-nnn]`

## Goal

Replace `task spawn`'s old window-name format (`<lowercased-title-with-dashes>[:30]`) with a short, scannable, ID-bearing format so that parallel spawned sessions are disambiguable at a glance.

## Final format

```
<project>_<one_or_two_words>[E-nnn]
```

Example: `endless_tmux-label[E-1144]`.

## Decisions made during implementation (and why)

### Separator: `_` (NOT `:` or `.`)

The original spec proposed `<project>:<slug>[E-nnn]`. Both `:` and `.` are tmux's target-spec delimiters:

| char | tmux meaning | failure |
|---|---|---|
| `:` | `session:window` | `tmux select-window -t 'sess:foo:bar'` parses as session `sess` + window `foo:bar`; window not found |
| `.` | `window.pane` | `tmux send-keys -t 'sess.foo'` parses as session `''` + window `sess` + pane `foo` |

`=`-prefix doesn't escape the separators (parsing happens first). Targeting by `pane_id` (`%N`) does work, but `tmux select-window -t <name>` (the literal copy-paste line we print after spawn) would still fail — that's a UX wound for the user, not just an internal one.

`_`, `-`, `~`, `|`, or no separator all work. Mike chose `_` (visually clear, no shell-quote issues, distinct from internal slug dashes).

### Slug heuristic: simple stop-list, deterministic

Title is lowercased, split on non-alphanumerics, filtered against this stop-word list (same as E-968's slug spec):

```
a an the to from of for with in on at by and or
```

First 1–2 remaining tokens are joined with `-`. Examples:
- "Change tmux window label..." → `change-tmux`
- "Add --text flag to endless task add" → `add-text`
- "Add --worktree flag to endless task spawn" → `add-worktree`

Trade-off: a fancier heuristic (drop verbs like add/change/fix; pick noun-iest words) would yield prettier slugs (`tmux-label` rather than `change-tmux`) but is non-deterministic across edge cases. Determinism wins here — the `[E-nnn]` already disambiguates.

### Removed 30-char cap

Old code truncated to 30 chars. The new format is intentionally short by construction; capping is unnecessary and would break the `[E-nnn]` suffix.

## Implementation

`src/endless/task_cmd.py`:
- New module-level `_SPAWN_WINDOW_STOP_WORDS` frozenset.
- New `_spawn_window_name(project_name, title, item_id)` helper.
- `spawn_plan` SQL extended to fetch `proj.name as project_name`.
- Window-name line replaced with the helper call.

## Manual test

Spawned a throwaway task E-1173 from inside the worktree:

```
PYTHONPATH=src endless task spawn E-1173 --worktree /tmp/e-1144-spawn-test --no-plan
```

Verified:
- New window appeared with name `endless_test-spawn[E-1173]`.
- `tmux set -w -t 'endless_test-spawn[E-1173]' @endless_task_id 1173` — exit 0.
- `tmux show -wv -t 'endless_test-spawn[E-1173]' @endless_task_id` returned `1173`.
- Cleaned up window + throwaway task + tmp files.

## Follow-up tasks filed

- **E-1173** (ready, phase=next): "Record Claude session UUIDs per tmux pane to enable crash recovery." Motivated by an unrelated tmux crash that occurred during this session, which exposed that nothing in Endless tracks the (tmux-pane → claude-session-UUID) mapping needed to resume sessions after a crash. Plan file at `~/.init/.endless/plans/E-1173.md`.
