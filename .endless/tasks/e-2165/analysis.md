## What is broken

`task update --type research` requires `--justification`. Supplying one is
refused when notes already holds a `## Justification` section, and nothing in
`task update` can edit or clear notes — `--clear` names description, plan,
analysis and outcome only.

So a task that has EVER been research can never become research again. The rule
actually enforced is "justification is write-once", which was never the intent:
it is a side effect of storing a required, validated field inside a free-form
prose column, where the only way to detect it is to parse a heading.

Found while retyping E-1921 back to research after it had been retyped to todo.

## Options considered, and why this one

  - **Make `--justification` upsert the section.** Smallest, and the common case
    just works — but it silently overwrites prose the owner may have hand-edited.
    Today's refusal exists to protect exactly that; it is an over-correction,
    not an accident.
  - **Accept an existing section as satisfying the requirement.** One line, and
    the round trip works immediately — but the stale justification then argues
    for research nobody is doing any more, and nothing prompts anyone to update
    it. A record that reads as justified by an argument that no longer applies
    is worse than the refusal.
  - **`--clear justification` (CHOSEN).** `--clear` already exists for this job
    on four other fields, so the vocabulary is not new. Deletion stays explicit,
    so no owner-written text disappears without someone typing the word, and
    re-justifying is then an ordinary `--justification` write.
  - **Give justification its own storage.** The real cause, and the durable
    answer — but a schema change should not ride along with a flag fix. It is
    already owned: E-1992's task_content work, migrated by E-1562.

## Scope

Extend `--clear`'s accepted values with `justification`, and make it remove the
`## Justification` section from notes while leaving the rest of the prose
intact. A task with no such section is a no-op, not an error.

Nothing here is specific to research: any future type with a required field
would hit the same wall, which is why this is the stopgap and E-1562 is the fix.

## Not in scope

  - Editing notes generally. This clears one structured section, not the column.
  - A `verb check` preflight for task titles. The adjacent friction found at the
    same time was a verb classified in the wrong category (`determine` as an
    action verb, discoverable only by hitting the refusal). Corrected by hand;
    owner's call that it is rare enough to keep handling that way.
