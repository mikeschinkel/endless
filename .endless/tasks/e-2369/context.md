$HOME is registered as an active project (id 63, name `mikeschinkel`, path `~`), dating from at least 2026-04-28. Nothing refuses it: register, init, discover and cwd resolution all treat $HOME like any other directory.

Effects seen:
- `endless project monitor` run from ~ resolved to the `mikeschinkel` project and launched against it.
- Repos under ~/Projects/*/ with no Endless config of their own walk up, find ~ registered, and take it as their project.
- ~/.endless/db-ledger/ holds stray events stamped project "mikeschinkel" (a phase change on E-1063 and the claim of E-2336), written from a cwd under ~ outside any real project.

On monitor: `project monitor --tmux` without NAME already refuses (verified from ~). Plain `project monitor` and `project status` default to the project enclosing cwd by design; that default is only harmful because ~ resolves to a project.
