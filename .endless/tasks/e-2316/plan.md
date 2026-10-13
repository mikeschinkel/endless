# Plan: show the claimed epic's children dimmed when stewarded elsewhere

## Problem

On a session-status board, `AnnotateSessionStatusStewardship` (`internal/monitor/session_stewardship.go`) suppresses any row whose steward is another present session. `isBoardFrame` exempts the focal task, its parent and its spawner, but not the focal task's children.

So a coordinator session claimed on an epic cannot see that epic's children whenever the session that filed them is still live. This happens to every epic carved out of a brainstorm: the brainstorm session files the children and then lingers. The rows are counted in "… N suppressed" and visible only with `--show-suppressed`.

## Design (Mike, settled in the E-2227 coordinator session)

The rule applies only when the board's claimed (focal) task is an epic, and only to that epic's direct children. Children of any other epic row on the board keep today's behavior.

For such a child:
- **No steward elsewhere:** shown normally, as today.
- **Stewarded by another present session:** SHOWN, rendered dim. It is not omitted and not counted in "… N suppressed". The dimming says another session is its steward.
- **Pinned by the viewer** (`session unhide --task`): shown normally, as today. The pin outranks stewardship.
- **Explicitly hidden by the viewer** (`session hide --task`): still hidden. An explicit hide wins.

Stewardship itself does not change: who stewards the task, the duplicate-work mark and ED-1605's ladder all stay as they are. This is display only.

## Steps

1. **Row data** (`internal/monitor/session_status.go`):
   - Add an in-query decoration `IsFocalChild` beside `IsParent`, true when the row's parent is the focal task and the focal task's type is `epic`.
   - Fill it in `SessionStatusRows` / `SessionStatusRowsForSession` with the other `IsFocal`/`IsParent` flags.
2. **Annotation** (`AnnotateSessionStatusStewardship`):
   - Where a row would get `StewardedElsewhere`, an `IsFocalChild` row instead gets a new flag, for example `StewardDimmed`. It does not get `StewardedElsewhere`, so the omission logic never sees it.
   - Document the flag on `SessionStatusRow` with the other E-2188 annotations.
3. **Render** (`internal/sessionstatuscmd/session_status.go`):
   - Rows with `StewardDimmed` are kept in the filter that drops `StewardedElsewhere` rows and are not counted as suppressed.
   - The whole row renders with the existing dim styling (`liveview.Dim`).
   - Use the `⊖` glyph in the hidden/suppressed column and the legend entry so the dimming is explained. Implementer's call is fine if a different existing glyph reads better; state the choice in the outcome.
   - `graph.go` treats `StewardDimmed` like the other non-graph decorations it already lists, if it needs to.
4. **JSON** (`internal/sessionstatuscmd/json.go`): emit `steward_dimmed`.
5. **Guide:** update the stewardship paragraph in the sessions guide section (find it with `endless guide sessions`) to say a claimed epic's children are shown dimmed rather than suppressed.

## Tests

- In `session_stewardship` tests, a child of a focal epic, filed by another present session:
  - gets `StewardDimmed`, not `StewardedElsewhere`;
  - is omitted when the viewer hid it;
  - renders normally when pinned.
- A child of a NON-focal epic row stewarded elsewhere is still suppressed.
- A child of a focal task that is not an epic is still suppressed.
- The render filter keeps a dimmed row and does not count it in the suppressed footer.
- `just test-go` and `just test` green.
