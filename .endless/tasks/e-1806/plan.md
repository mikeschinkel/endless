# Rewrite the 'completed' status-gate error to drop internal jargon

## Symptom

Attempting `endless task complete <id>` on an implementation task (lead verb not
completable) prints:

    Status 'completed' requires a completable lead verb in the task title.
    Title's lead verb is 'show', which is not marked `completable: true` in
    verbs.json.
    Completable verbs (e.g. audit, research, investigate, review, analyze)
    signal that the deliverable is text/findings, not behavior. For
    implementation tasks, use 'unverified' → 'confirmed' or 'assumed' instead.

This leaks internal mechanism into a user-facing error: `verbs.json` and
`completable: true` are implementation details a user should never have to know,
and the message is long and technical for what is a simple "wrong final status"
correction.

## Deliverable

Replace the message raised by `_require_completable_verb_for_completed`
(`src/endless/task_cmd.py`) with plain-language wording that states intent +
action and names no internal mechanism:

    Error: 'completed' isn't a valid final status for this task. Implementation
    tasks finish as 'confirmed' or 'assumed'.

- No `verbs.json`, no `completable: true`, no "lead verb" phrasing.
- Keep the gate behavior itself unchanged — this is wording only.

## Relation to E-1658

E-1658 replaces the `completable` verb flag with a verb `category` +
per-task-type `accepts` mapping, and will rework this exact gate. If E-1658
lands first, fold this wording change into it rather than shipping twice. This
task stands alone only so the jargon leak is fixed promptly if E-1658 stays
blocked.
