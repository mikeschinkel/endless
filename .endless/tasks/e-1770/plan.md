# E-1770 — Remove the tmux return-line and spawner-task identity from spawn handoffs

## Context

Handoffs are meant to be lean. Two vestiges now generate noise:

- Every template opens with "Your spawning session's task is: E-N — return to that
  session with `tmux switch-client -t <anchor>`."
- Every final message closes with a prominent
  "`tmux move-window -t archive: && tmux switch-client -t <anchor>`" return line.

Both predate `session monitor` (which shows the spawning task) and
`session goto <task>` / `session back` (how a user returns). Navigation is a user
concern served by the tmux status line and the CLI — the agent should not emit
per-handoff hints for it. Remove it all. **Pure removal; no `session goto` pointer.**

The identity ("Your spawning session's task is: E-N") is itself the noise
`session monitor` renders, so `spawner_task` joins `return_anchor` as a fully-dead
template input. Full decouple removes both, leaving neither `{{.return_anchor}}` nor
`{{.spawner_task}}` anywhere.

## Deliverables

### 1. Templates — `internal/templatecmd/templates/handoff/`

- **`_close.tmpl` (`handoff_close`):** delete the non-bg `{{else}} Make the return
  line prominent:` branch and its ` ```tmux move-window … switch-client -t
  {{.return_anchor}}``` ` block. Keep the bg `claude attach` note. Non-bg then appends
  nothing after the "do NOT confirm the negative" sentence.
- **task / bug / epic / research / brainstorm:** collapse the
  `{{if .bg}}…{{else}}…{{end}}` spawning-task block to a **bg-only** orientation line —
  drop the "Your spawning session's task is: E-{{.spawner_task}}" identity and the
  "there's no pane to switch back to" clause from the bg branch, keeping the per-type
  verb ("work the task" / "run the brainstorm" / "coordinate the epic") + "stop when
  done. The user follows your progress with `claude attach`." Delete the non-bg
  "…return to that session with `tmux switch-client -t {{.return_anchor}}`." sentence
  entirely (non-bg renders nothing there).
- Drop the step-5 `{{if not .bg}} (with the return line above){{end}}` parenthetical
  (task/bug/epic/research) and brainstorm's line-23 equivalent → uniform
  "STOP and ask me. Don't guess." / "let the requester choose. Don't decide for them."
- **`respawn.md.tmpl`:** delete line 51 ("Return to the spawning session with
  `tmux switch-client -t {{.return_anchor}}` when you need me.").

### 2. Plumbing — `src/endless/task_cmd.py`

- Remove the `return_anchor` and `spawner_task_id` parameters (~4092–4093) from the
  handoff-vars builder and the `"return_anchor"` / `"spawner_task"` entries in the vars
  dict (~4149–4150).
- Remove the stale comment at ~4912 ("branch omits the tmux return lines, so
  return_anchor is unused").
- Trace the callers (spawn foreground / `--bg` / `task handoff`) and drop the
  now-unused arguments. Decouple completely — no dead params left behind.

### 3. Tests

`tests/test_handoff.py`, `tests/test_handoff_children_state.py`,
`tests/test_template_materialize.py`, `tests/test_handoff_internal_cli.py`: remove
`return_anchor` / `spawner_task_id` / `"spawner_task"` args and any assertions on the
return line or spawning-task identity. Add a regression assertion that a rendered
handoff contains NEITHER "switch-client" NOR "spawning session's task".

### 4. Guide — `docs/guide/orchestration.md`

- ~Line 218: drop "the spawning session's task," and "the `tmux select-window` line
  back to your window," from the handoff-contents sentence.
- ~Line 222: drop "and return line" from the final-message discipline description.

### 5. Verification — `tests/tasks/e-1770-verify.sh`

Self-contained (model on `tests/tasks/e-1607-verify.sh`). Build the worktree
`endless-go`; render all six templates in both fg and bg and assert:

- no rendered handoff contains `switch-client`, `move-window`, `return to that
  session`, `return line above`, or `spawning session's task`;
- bg handoffs still carry the "headless background agent" + `claude attach`
  orientation;
- `docs/guide/orchestration.md` no longer mentions the return line, the
  `tmux select-window` line, or "the spawning session's task";
- grep of the templates shows no `{{.return_anchor}}` / `{{.spawner_task}}` remain;
- fold-in regression: `go test ./internal/templatecmd/...` and a targeted
  `uv run pytest` over the four handoff test files pass (targeted, not `just test`, to
  stay off the unrelated guide-map coverage gate).

## Notes

- `session goto` / `session back` / `session monitor` are the user-facing
  replacements; they are intentionally NOT referenced from the handoff (navigation is a
  user concern). Their guide coverage is stale — tracked as a separate sessions-guide
  refresh task.

## Verification (handoff to Mike)

Set `unverified`; run `esu && ./tests/tasks/e-1770-verify.sh`.
