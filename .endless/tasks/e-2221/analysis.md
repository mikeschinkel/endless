## Why the mark changes basis

Today ◫ (`DuplicateWork`, internal/monitor/session_stewardship.go) comes from
who filed, updated or focused a task, read from `session_tasks` in one query.
A task that is on a board as a dependent, blocker or child has no such row, so
a task listed on two boards went unmarked (E-2519 on tasks:9 and tasks:10,
2026-10-31). Mike designed ◫ to mean "listed in another session", which needs
other boards' rows, and that is the new cost.

## Why build other boards only while drawing

Mike: before E-2229 gated monitors on window visibility, monitors maxed the CPU
at 15–20 sessions; with it, 70+ sessions run with minimal CPU. A background job
building every board on a timer would reintroduce always-on work. A monitor
recording what it drew fails because a hidden monitor draws nothing.

## Why planning, not focus, suppresses

Focus moves on any write, and sessions routinely add notes and links to other
sessions' tasks. Suppressing on focus would take a task off its steward's board
after a note. Mike chose (Q6) to key suppression on E-2533's planning marker.
He kept focus and stewardship separate, wary of unintended consequences of
tying them.

## Why only unspawned tasks

Mike: "I won't be spawning any already spawned/underway (or later)" tasks; a
held task already says where it is worked.

## Glyph for the filing task

✚ reads as "created", is unused in Endless, and renders at normal width. ⊞ was
rejected: it resembles ⊕ ⊗ ⊘ ⊖, which already mean other things. Mike chose
glyphs over words: words help a new user but are slower to scan, and users
become experienced with the monitor quickly.
