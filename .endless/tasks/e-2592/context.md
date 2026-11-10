E-2369 refuses directories that cannot be projects: $HOME and above, per-OS system, temp and user-data directories, parent directories like ~/Projects, and nested projects. Its rules are absolute paths (/private, /tmp, /usr/share, ~/Downloads) checked against where a directory really is on disk.

A test's fixtures live in a temp directory, which those same rules refuse (/private on macOS, /tmp on Linux). E-2369 lets tests through by having each fixture write a `remove` entry for the temp-tree rules into its own overrides file. That works, but it switches off the very rules a test might want to exercise, and a macOS run can never exercise the Linux rules (or the reverse).

Mike, 2026-11-09: commands that resolve directories should take a --base-dir that defaults to /, separating the logic applied to the directory under test from the isolation the test creates.
