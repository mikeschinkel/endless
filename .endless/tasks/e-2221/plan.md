## Terms

- **Board**: the rows one session's `session status` / `session monitor` shows.
- **Listed on a board**: a row that board actually shows, after its own hides
  and suppression. A row a board suppresses is not listed there.
- **Present session**: the presence test stewardship already uses
  (`presentSession`): a live recorded state and a liveness that is not `dead`.
- **Unspawned task**: a task no session claims or binds, whose status is
  before `underway` (unplanned, submitted, ready, revisit).
- **Planning session**: the session E-2533 records as planning a task.

## ◫, the duplicate mark

◫ tells Mike a task is listed in at least one other session, so he does not
act on it in both. It goes only on unspawned tasks: a task some session claims
or binds is never ◫. On its holder's board it is row one, `●`; on any other
board it reads as worked elsewhere (`⟳ doing`), as today.

On a board B, an unspawned task's row:

1. **B's session is its planning session** — shown, no ◫.
2. **Another session is planning it** — suppressed, unless B's session touched
   it, in which case shown with ◫.
3. **Another present session stewards it** — suppressed, unless B's session
   touched it, in which case shown with ◫. (Today's rule; E-2414 P27 keeps
   touched rows.)
4. **Otherwise** — shown, with ◫ when it is listed on any other present
   session's board.

Focus (`◼︎`) becomes the highlight only: it no longer sets ◫, and no longer
keeps a row on the board against suppression. Rule 2 replaces what focus did.

Suppressed rows stay in the graph wherever they block, or should precede, a row
the graph draws (E-2455's predecessor walk); no graph change is needed.

## Listed elsewhere: how it is worked out

A board that is being drawn builds every other present session's board, with
that board's hides and suppression applied, and collects the ids each one
lists. This happens only while drawing: a hidden `session monitor` draws
nothing (E-2229), so cost scales with visible monitors, not sessions. There is
no background job and nothing is stored.

Building every board needs each board's own suppression, and suppression
(rules 2 and 3) depends on stewardship and planning, never on what other
boards list, so there is no cycle: compute each board's shown rows first, then
◫ from the union of the others.

Measured on a copy of the main database (2026-11-01): 157 live-state sessions
built in 2.4 s, about 15 ms each; about 50 sessions were running. Build only
present sessions' boards.

## The parent, spawner and filing tasks

They leave the task rows. A board with a claimed or bound task shows them on
its second line, directly under the `●` row:

    ↑ E-XXXX  ↩ E-YYYY  ✚ E-ZZZZ

- `↑` parent: the claimed task's effective parent (as the ↑ row uses today).
- `↩` spawner: the spawning session's task (as the ↩ row uses today).
- `✚` filing task: the task the filing session held when it filed the claimed
  task — what `task show` prints as `Created … by ES-N (E-M)`.
- A task in several roles appears once per role.
- An entry whose task has no present session holding it is dimmed.
- An absent role is omitted; with none, the line is omitted.
- None of them ever carries ◫.
- They keep whatever place in the graph they have today.

The legend gains `✚ filed`. `--json` carries the three ids on the frame.

## Order

Blocked by E-2414 (rewrites the same annotation and retires pins) and E-2533
(the planning marker rules 1 and 2 read).
