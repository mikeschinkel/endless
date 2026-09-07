# Audit and finish the `--text` → `--all-fields` sweep

## Problem

A task's design content routinely lives in `--analysis`, and many tasks have no
plan in `--text` at all. `endless task show <id> --text` renders analysis as a
one-line teaser (`Analysis: 3270 chars (--analysis to display)`), so a spawned
session that follows its handoff literally never reads it. Observed on E-2122:
the analysis held the fix direction, a second call site with the same defect, a
separate `REBASE_HEAD` defect, and an explicit "do not spend effort
reconstructing this one instance" that contradicted the handoff's "reproduce the
bug first" — none of it in the description. Tasks have been planned with
substantive content in `--analysis` and then spawned on the belief it was being
read. It was not.

The commit titled "Change --text to --all-fields" started the fix by hand.
This task audits that commit: finish what it missed, revert what it should not
have touched, and leave the write-flag sites alone.

Find the commit with `git log --oneline --all --grep='Change --text to --all-fields'`,
and read it with `git show --stat` before starting. Everything below was true of
that commit; re-derive it against current `main` rather than trusting it.

## The trap: two different flags spell themselves `--text`

- **Read flag** — `task show --text`, a display toggle. This is the one that
  should become `--all-fields`.
- **Write flag** — `task add`/`task update --text <content>`, `--text-file <path>`,
  `lesson write --text`. Sets `tasks.text`. Must **never** be touched.

The claim handoff template contains **both on one line**: it tells the session to
fold its context in with `task update ... --text <path> --db main` and then to read
the task with `task show ... --text --db main`. Any sweep that matches the string
`--text` without reading the verb in front of it will corrupt the write sites.
Do not do this with a blanket `sed`.

The discriminator is the verb: `show` → change, everything else → leave.

## Verified inventory

### A. Missed — must change (the actual bug)

The handoff templates under `internal/templatecmd/templates/handoff/` render every
spawn and claim prompt. They are the reason sessions read `--text`, and the commit
did not touch any of them. Find them with:

    grep -rn 'task show' internal/templatecmd/templates/handoff/

Expect six files: `todo`, `bugfix`, `research`, `epic`, `brainstorm`, `claim`
(all `.md.tmpl`). Each becomes:

    endless task show E-{{.spawned_id}} --all-fields --db main

Per-file notes:

- `brainstorm.md.tmpl` reads "to read the seed/framing" rather than "to read the
  plan" — change only the flag, keep the wording.
- `claim.md.tmpl` — change **only** the `task show` occurrence. The
  `task update ... --text <path>` on the same line stays exactly as it is.

Also missed, in `docs/guide/index.md`: the read-a-task-you-didn't-claim example
under the hand-off-to-another-session comment block, currently
`endless task show <id> --text --children --llm`. Find it with:

    grep -n 'task show <id> --text' docs/guide/index.md

`--all-fields` already includes children, so the recommended result is
`endless task show <id> --all-fields --llm`. If the reviewing session disagrees
about dropping `--children`, keeping it is harmless — say which was chosen.

### B. Correct as changed — keep

- `docs/guide/index.md`, happy-path step 1 ("read the task and any attached
  plan"). This is the guide instruction sessions are pointed at; correct.
- `docs/guide/tasks.md`, the `task show` example block — an additive
  `endless task show <id> --all-fields` line. Correct. Consider listing
  `--all-fields` first, as the default move.

### C. Should be reverted — inert

In `internal/hookcmd/claim_handoff_test.go`, inside
`TestHandlePostToolUseSession_NonClaimYieldsNoHandoff`. That test's fixture is a
list of commands that are **not** a claim and must yield `""`. The flag on that
string is arbitrary: changing it asserts nothing, fixes nothing, and makes the
commit look as though the handoff path was covered when it was not. Revert to
`--text`.

If the reviewing session judges the string better left as `--all-fields` for
consistency with the new guidance, that is defensible — but say so explicitly
rather than leaving it unexamined.

### D. Must NOT change — write-flag sites

Every other `--text` / `--text-file` hit in the repo is the write flag or prose
about it. Confirm they are left alone. The sites cluster in:
`src/endless/task_cmd.py`, `src/endless/cli.py`, `src/endless/lesson_cmd.py`,
`internal/hookcmd/claude.go`, `internal/events/`,
`internal/templatecmd/claim_handoff_test.go` (that one is
`task update --text <path>`), `docs/guide/tasks.md`,
`docs/guide/orchestration.md`, `docs/guide/reference.md`, `CLAUDE.md`, and the
`tests/` files that exercise the write flag.

### E. Judgment call — recommend keeping as `--text`

In `src/endless/worktree_cmd.py`, the plan-divergence message emitted when the DB's
`tasks.text` and the branch's plan file disagree. It offers
`View full: endless task show E-<id> --text`. Find it by searching for the
`View full:` literal. Here `--text` is the point — the operator is being shown the
one field under comparison, and widening it to every field dilutes the message.
Recommend keeping; state the decision either way.

## Coupled tests — will fail when A lands

`tests/test_handoff.py` pins the current wording in two assertions. Find them:

    grep -n 'task show E-.* --text' tests/test_handoff.py

Update both to `--all-fields`. These are the reason the template change cannot be
made blind: they pin the old string, so a green suite after touching only docs and
an inert test string is not evidence the handoff was fixed.

Before assuming those two are the whole set, check the Go side for golden or
template tests pinning the same strings:

    grep -rn 'task show E-' internal/ tests/

## Steps

1. Re-derive the inventory against current `main`. Find sites by reading the verb
   in front of the flag, never by matching the flag alone.
2. Apply A — the handoff templates, plus the `docs/guide/index.md` read example.
3. Apply C — revert the inert test change, or justify keeping it.
4. Update the `tests/test_handoff.py` assertions.
5. Confirm B and E, and confirm D is untouched: `git diff --stat` should show no
   churn in `src/endless/task_cmd.py`, `cli.py`, `lesson_cmd.py`, or
   `internal/events/`.
6. If the guide is generated or indexed, re-run `just guide-index` and commit the
   artifact.

## Verification

- `just test` and `just test-go` green.
- Render the handoff for each of the five spawn types plus claim; each shows
  `--all-fields --db main` and no bare `show ... --text`.
- `git diff main --stat` touches only: the handoff templates,
  `docs/guide/index.md`, `docs/guide/tasks.md` (if reordered),
  `tests/test_handoff.py`, and `internal/hookcmd/claim_handoff_test.go`.
- Grep the diff for `--text-file`: there must be zero hits.

## Out of scope

Whether `task show` should surface analysis by default, or warn when a populated
field is being hidden behind a flag, is a separate design question. This task only
makes the instructions we hand sessions point at every field.
