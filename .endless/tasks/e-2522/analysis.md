Decided with the owner (2026-11-01):
- Drop `project register` outright: no deprecation period and no message pointing at `project init`.
- Rename `project unregister` to `project deinit`, so the command that undoes `init` is named for it.
- Replace "registered" for projects with three states: noted (a database row the monitor added, never initialized), initialized (`project init` or `project discover` ran), ignored (`project deinit` ran). "Discovered" was rejected because `project discover` produces initialized projects. "Recorded", "logged", "reported", "filed" and "scanned" were rejected because each already names another Endless concept; "listed" because every project appears in `project list`. "Uninitialized" is not needed: deinit's result is the existing state "ignored", and "uninitialized" already means a database with no schema.
- The config value written by deinit is "ignored". Configs holding the old value "unregistered" are not read specially (none exist on the owner's machine).

Find sites with: git grep -in 'regist' -- src internal cmd docs/guide README.md tests
