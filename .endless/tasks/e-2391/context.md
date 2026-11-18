Today a session touching tasks in several projects can't tell which project a row belongs to.

The project code is 1-4 uppercase letters, e.g. E for endless, CLI for go-cliutil, CFG for go-cfgstore. When a session's task list spans more than one project, session monitor and session status show a project column to the left of the task type. When all tasks share one project, the column is hidden to save space.

Related plan: task IDs will move to <prefix>-<id>, where the prefix is this project code and the id may be a number or a hash depending on the install.

Nothing stores a project code yet, so this task also adds it: the project field, a way to set it, and showing it on the project's registration card. E-2652's console status row reads the code too, as `endless (E)` and `go-cliutil (CLI)` (Mike, planning E-2652).
