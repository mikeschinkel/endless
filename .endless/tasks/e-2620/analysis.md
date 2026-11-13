Chosen with Mike (2026-11-13), option A of one question: commit
`lefthook.yml` before init's other commits, so every commit init makes
contains it. When those commits are replayed onto a base without it, as a
`git pull --rebase` onto a GitHub-created initial commit does, the
`prepare-commit-msg` hook then always finds `lefthook.yml`, and lefthook's
hook-sync step stops printing "no lefthook config found".

Rejected: declining the task as cosmetic. The message looks like a failure
and gets reported.

Known gap, accepted: commits the user made before running init, replayed
onto a base without `lefthook.yml` after hooks are installed, still print
the message. Endless cannot add a file to commits it did not make.

Checking for remote commits before init would not have helped: in the
go-filesys case, the GitHub repo was created after init ran.
