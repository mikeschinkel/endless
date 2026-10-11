# `task sessions <id>` — who holds this task, and is it live

## Why a command rather than reading `task show`

`task show` already renders a "Touched by" block: each session that touched the
task, its relation (Claimed / Surfaced / Revisited), and its state in brackets
(`working`, `idle`, `ended`). That is most of the data and none of the answer.

The question that arises when the duplicate glyph appears is "which session
should I go to, and is there actually something running there" — and the missing
fact is whether a live tmux window holds a Claude session for that task. A
session row can read `working` while its pane is gone; `session list` knows about
panes but cannot filter by task and shows no relation.

So the data exists in two commands and the answer is in neither. One command
that answers it, rather than a third place that half-answers it.

Checked before filing: no existing command does this. `session list` lists
sessions with their active task but takes no task filter and reports no relation;
`session show` is per-session; `session goto` navigates to one pane but does not
enumerate; `task show`'s block is described above.

## The command

`endless task sessions <task-id>` — one row per session holding the task:

- session id
- relation (claimed / surfaced / revisited / referenced / queued)
- session state (working / prompted / idle / needs_input / ended)
- whether a live tmux window holds a Claude session for it, and which pane
- whether this is the session's own task or only its focus pointer

Ended sessions are omitted by default and included with `--all`: the usual
question is who holds it NOW, and the touch history is what `task show` is for.

`--json` for the machine-readable form, matching the other read commands.

The liveness probe is the one new fact. Reuse what the status monitor already
does to decide a pane is gone rather than writing a second probe — one answer to
"is this pane live", in one place.

Report the raw state from the `session_liveness` view — `live`, `unknown`,
`dead`, `unbound` — not a yes/no. Since E-2293 two rules read that view
differently: the claim guard treats `unknown` as live (a wrong refusal costs a
retry), while the status monitor counts a session toward suppression only when
it is `live` (its pane was seen). A collapsed yes/no would disagree with one of
them. The case to get right is a session bound to a tmux server that no longer
exists: it reads `unknown` forever and must show as such.

## `task show` gains the same fact

The "Touched by" block gets the live-window column too. It is the same question
asked in passing, and a reader comparing the two must not get different answers.
`task sessions` stays the command for the full picture — relation, pane, focus
versus own task, and the `--all` history.

## Name

`task sessions`. It names what it lists. `task dups` and `task find-dups` were
considered and rejected: duplicate holding is the symptom that sends you here,
not what the command reports, and a command named for one symptom will be looked
past when the question is merely "where is this task running".

## Acceptance

- `task sessions E-NNNN` lists every non-ended session holding the task with
  relation, state, live-window status and pane, and whether the task is the
  session's own or only its focus.
- `--all` includes ended sessions.
- `--json` emits the same rows.
- A session whose pane is gone is reported as not live even when its row still
  says `working`.
- A task no session holds says so rather than printing an empty table.
- `task show`'s Touched-by block reports live-window status, and agrees with
  `task sessions` for the same task.
- `just test` and `just test-go` pass.

