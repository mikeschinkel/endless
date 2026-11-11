go-fsfix (~/Projects/go-pkgs/go-fsfix) builds test file trees: RootFixture, RepoFixture, DirFixture, FileFixture. It predates t.TempDir(): NewRootFixture makes its own os.MkdirTemp directory and the test must defer Cleanup(). A test may create several root fixtures; each gets its own temp directory.

Endless's Go tests do not use go-fsfix. They build their trees by hand with t.TempDir(), os.MkdirTemp and per-package helpers. Mike, 2026-11-11: tests that hand-roll fixtures should move to go-fsfix for consistency, and the move should surface what go-fsfix cannot handle yet. Python tests stay as they are until E-1063.

E-2592 adds a --base-dir that reads the directory rules relative to a base directory, and requires every directory a rooted test checks to sit inside it. How a test's fixtures relate to its base directory, especially with several root fixtures in one test, is open.
