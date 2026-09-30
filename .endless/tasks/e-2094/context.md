E-2023's runner hands every suite a temp HOME from os.MkdirTemp, and on macOS the OS temp root sits beneath a symlinked prefix (var -> private/var) — so every suite runs under a HOME reached through a symlink, which a real home directory almost never is.

That is an artifact of the isolation MECHANISM, not a property of the code under test; it is platform-dependent (passes on Linux, fails on macOS); and it makes suites fail for reasons unrelated to their own task, which is the one thing a verification front door must never do.

The whole internal/monitor package then passes under a temp HOME, so the planned per-test fix and the sweep for other HOME-sensitive tests both become unnecessary.
