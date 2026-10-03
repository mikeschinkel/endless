# Align ownership wording with ED-1605

ED-1605 fixes the vocabulary: owner (the Endless session that claimed the task),
the owner's current Claude session (always written in full), steward, worktree
lock holder; landing is done from the owner's side; "own" means nothing else.
Bring every existing use into line.

1. **ED-1560** — "A session owns exactly one task" becomes "claims". Edit the
   decision's description with `endless decision update`.
2. **ED-1530** — "write-often OWNERSHIP (session_id, pid, …)" becomes the
   worktree lock holder.
3. **E-2188's "owner" becomes steward:**
   - internal/monitor/session_ownership.go: comments, `taskOwnership`,
     `owner()`, `liveOwnership`, `OwnedElsewhere` (rename the file to
     session_stewardship.go);
   - the `owned_elsewhere` JSON field on session status rows becomes
     `stewarded_elsewhere`;
   - docs/guide/sessions.md, "Focus, and a task on more than one board" —
     retitle without "board" and use steward throughout.
4. **Handoff templates** — replace "The spawning session owns landing." in
   internal/templatecmd/templates/handoff/{todo,bugfix,epic,brainstorm}.md.tmpl
   and the matching line in _mechanics.tmpl with the landing rule: the work is
   landed from the owner's side, by the user in a tmux sibling pane; do not run
   `worktree land` yourself.
5. Sweep `src/`, `internal/`, `docs/` and the templates for any other "own",
   "owner" or "ownership" about tasks or sessions, and align it.

Blocked by E-2204, which edits session_ownership.go first.

## Verify

A grep for own/owner/ownership in the touched areas returns only ED-1605's
meanings; `session status --json` carries `stewarded_elsewhere` and not
`owned_elsewhere`; a rendered handoff carries the new landing line; the
existing E-2188 and E-2204 tests pass under the new names.
