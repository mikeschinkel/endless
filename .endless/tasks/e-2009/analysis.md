SETTLED (was an open design question, resolved by Mike on 2026-08-20): outside-$HOME IS refused. The earlier worry that this breaks a user with projects on an external volume or under a system directory stands as a caveat for the config-extensible half — the built-in list is the guard against accidental assignment, and config is how a legitimate non-standard layout opts back in. Design the refusal so it can be overridden per project, not so it is unreachable.

Resolve symlinks before testing against the list (monitor.NormalizeProjectPath in Go, endless.project_path.normalize in Python, both from E-2002), or the temp-dir entry misses on macOS where the temp dir is a symlink into a private tree — the same mismatch class E-2002 fixed.

The GOOS-specific default list is what makes this shippable for someone who is not Mike: container directories differ per platform, and a hardcoded macOS list would silently do nothing on Linux.

## The worktree case belongs on this list too (found 2026-08-23, from E-2030)

Four rows — `e-1733`, `e-1914`, `e-1917`, `e-1920` — had been auto-registered as
standalone projects from `~/Projects/endless/.endless/worktrees/*`. Same defect
as $HOME, different directory class, needing the same guard, so it is folded in
here rather than filed separately.

Two independent paths create them, and BOTH need the guard — fixing one leaves
the other:

  * Go. `ProjectIDForPath` walks up ancestors for a registered project and, on a
    total miss, calls `ensureAutoRegisteredProject(dir)`, which registers
    `filepath.Base(dir)` with no test of what `dir` is. The miss happens exactly
    when the enclosing project is absent from the DB.
  * Python. `reconcile.py` scans roots for any directory containing
    `.endless/config.json` and treats each as a project. A git worktree ALWAYS
    contains one, because `.endless/` is tracked — so the scan finds a project
    per worktree by construction, with no bug required anywhere else.

The rule is cheap and exact, and unlike the $HOME entry it needs no config
override: a directory at or beneath a known project's `.endless/worktrees/` is
never itself a project. Endless creates those directories, so their shape is not
a guess about the user's layout.

PRODUCT: this is not self-dev-only. Every Endless user gets per-task worktrees
under `.endless/worktrees/`, and every one has `.endless/` tracked in git, so any
user running `project scan` over a directory holding a tracked project gets one
phantom project per open task.
