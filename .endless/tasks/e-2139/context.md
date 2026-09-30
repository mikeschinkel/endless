When a change on main requires every other session to do something -- resolve a mechanical rebase conflict, adopt a moved path, rerun a build step -- there is no way to tell them.

Each session discovers it alone, at its next rebase, as an unexplained conflict, and works out the resolution from scratch.

The restructure in E-2138 is the immediate case: it renames files 51 worktrees hold commits against, and the resolution is one mechanical step that the session causing it already knows and cannot pass on.
