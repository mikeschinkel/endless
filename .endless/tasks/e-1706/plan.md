# Reassign session-status type letters (brainstorm Z→B, bug→F)

## Why
In `session status`, the type letter for `brainstorm` is `Z` (weak mnemonic) and `bug` is
`B`. Give brainstorm the better `B`, and move bug to `F` (Fix). Pure display — no stored data
changes, no upcasting.

## Change
`typeLetter(slug)` in `internal/sessionstatuscmd/session_status.go` (the ONLY letter-render
site — verified):
- `brainstorm` → `B` (was `Z`)
- `bug` → `F` (was `B`)
- unchanged: `epic`→`E`, `research`→`R`, default (task/todo)→`T`

Resulting letters `E / F / R / B / T` are collision-free; `Z` is freed.

## Coordination with E-1659
E-1659 renames the type *slug* `bug`→`bugfix` (and `task`→`todo`), blocked by E-1671's
upcasting pipeline. That's the NAME; this is the LETTER, and they're independent. When E-1659
lands, its rename flips this function's `case "bug"` label to `case "bugfix"` but keeps `→F`.
Letters need not wait on the pipeline.

## Verify
`endless session status` renders a brainstorm task with a leading `B` type letter and a bug
task with `F`. Update/confirm the typeLetter unit test if one exists.
