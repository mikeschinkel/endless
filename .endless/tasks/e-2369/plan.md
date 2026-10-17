# Plan: decide which directories may be Endless projects

## Rules

Each list entry has a scope:
- **self**: the directory itself can never be a project. A child can.
- **subtree**: neither the directory nor anything under it can be a project.

1. **Never a project, not overridable.** $HOME itself (self), and every ancestor of $HOME (self): /Users, /home, C:\Users, `/`, a drive root. Directories outside $HOME stay allowed (/work/my-project in Docker, VMs, CI/CD).
2. **Built-in per-OS list.** Overridable (see below).
   - **Linux (FHS)**
     - subtree: /bin, /sbin, /lib, /lib32, /lib64, /libx32, /boot, /dev, /proc, /sys, /run, /etc, /usr/bin, /usr/sbin, /usr/lib, /usr/lib64, /usr/libexec, /usr/include, /usr/share, /var/lib, /var/log, /var/cache, /var/spool, /var/tmp, /lost+found
     - self: /usr, /usr/local, /var, /var/www, /opt, /srv, /mnt, /media, /tmp, /root. Children are allowed: /opt/app, /srv/site and /var/www/site are real project homes.
     - desktop (XDG): ~/.config, ~/.local and ~/.cache as subtree. The user dirs (Desktop, Documents, Downloads, Music, Pictures, Videos, Public, Templates) as self. Ubuntu, Fedora and other desktop distros create them through xdg-user-dirs. They are read from `~/.config/user-dirs.dirs` when present, because their names are localized (~/Dokumente on a German desktop). They are skipped when absent, as on servers.
   - **macOS**
     - subtree: /System, /Library, /Applications, /private, /bin, /sbin, /usr/bin, /usr/sbin, /usr/lib, /usr/libexec, /usr/share, /etc, /var, /dev, /cores, ~/Library, ~/Applications, ~/.Trash
     - self: /usr, /usr/local, /tmp, /opt, /Volumes, ~/Desktop, ~/Documents, ~/Downloads, ~/Movies, ~/Music, ~/Pictures, ~/Public
   - **Windows**
     - subtree: C:\Windows, C:\Program Files, C:\Program Files (x86), C:\ProgramData, C:\Users\Public, C:\Users\Default, ~\AppData. Program Files is right to refuse whole: it is written by installers, needs elevation to write, and UAC virtualizes writes to it.
     - self: ~\Desktop, ~\Documents, ~\Downloads, ~\Music, ~\Pictures, ~\Videos, ~\OneDrive
     - Known folders are resolved through the Windows known-folder API, not hard-coded, because they can be relocated (OneDrive redirection, a different system drive).
   - **Localized names are the OS's job.** Endless keeps no translation tables:
     - Linux: xdg-user-dirs writes the real localized paths into `~/.config/user-dirs.dirs`, and Endless reads that file.
     - macOS: the folders are always English on disk; Finder only translates the display name.
     - Windows: the known-folder API returns the real path.
   - **User-data folders are self-scoped on purpose.** Many people keep projects in ~/Documents/foo rather than ~/Projects/foo, so the children of ~/Documents, ~/Desktop and similar stay allowed. Only the folder itself is refused.
3. **Parent directory.** Not a project itself, and the walk-up stops there, so nothing below inherits a project registered above. ~/Projects ships as one. A directory is declared a parent directory either way:
   - a JSON override entry with `"parent_dir": true`
   - an on-disk `.endless-parent-dir` marker file. At the same rung the marker wins, as `.endless-ignore` does.
4. **No nested projects.** Register, init, discover and the hook's auto-registration refuse a directory that has a registered project above it, or below it, naming that project. Ignored rows and disallowed rows do not count as projects. Worktrees are not registered projects, so they are unaffected. The release valve for git submodules is the separate maybe task E-2372.

## Where the list lives

- Built-in defaults live in the Go binary, per OS.
- `~/.config/endless/disallowed-paths.json` (`monitor.ConfigDir`) holds only overrides: additions (with scope and `parent_dir`) and removals of built-in entries. It is loaded on demand, and created on first need as an empty skeleton. New defaults reach every user automatically. Rule 1 cannot be removed. Removing a built-in entry is how someone who tracks /etc with git, for example, opts back in.
- The full built-in list, every OS, is generated into `docs/guide/disallowed-paths.md`, shown by `endless guide disallowed-paths`, with a `just` regen recipe and a drift check in `just test`, following lifecycle-check.

## Enforcement point

`monitor.ResolveDirectory` (internal/monitor/project_ignore.go), the one resolver behind hook auto-registration and Python register, discover and reconcile (via `endless-go project resolve`).

- A new `VerdictDisallowed` carries the matching rule. Callers treat it like ignored: never auto-register, record nothing.
- The walk never matches a row at a disallowed rung, and stops at a parent directory. `deepestRowForResolvedPath` applies the same rules.
- The nested check (rule 4) is a resolver query, so all registration paths share it.
- Python refuses with the rule and the overrides file named. `project_name_for_cwd` skips `.endless/config.json` at a disallowed directory. There is no new SQLite in Python.

## Existing rows

- A row at a disallowed path goes inert on landing, and `project list` marks it.
- `project unregister` on a disallowed row deletes the row rather than marking it ignored. An ignored row at $HOME would otherwise read as "ignore everything under ~".
- **Reminder for Mike after landing:** run `endless project unregister mikeschinkel` to remove the $HOME row.
- Already-nested rows are left in place and marked in `project list`. On this machine, the only nesting is the $HOME row.

## Tests

Go table tests with a temp $HOME, a temp config dir, and the OS list injectable:
- rule 1, including outside $HOME → allowed
- self vs subtree (/opt vs /opt/app, /usr/lib/x refused)
- localized XDG dirs from user-dirs.dirs
- override add and remove; removing $HOME ignored
- parent directory via JSON and via marker
- `.endless-ignore` and ignored rows still win
- nested above and below refused; ignored rows don't count
- the hook in $HOME creates nothing
- `unregister` deletes a disallowed row

Python tests: register on $HOME, ~/Documents and a nested directory refuses; discover never offers them. Doc drift check.

## Verify

`just test` and `just test-go` pass. From ~, `endless project status` refuses cleanly. `endless guide disallowed-paths` prints the list.
