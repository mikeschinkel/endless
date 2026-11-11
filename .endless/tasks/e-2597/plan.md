Open questions:
- Should RootFixture build on t.TempDir(), taking t at construction and dropping the explicit Cleanup()?
- With several root fixtures in one test, should they share a parent directory, and should that parent be available as the test's base directory for E-2592's --base-dir?
- What do Endless's hand-rolled Go fixtures do that go-fsfix cannot (symlinks, relative links, a fake HOME, overrides files, git repos with commits, subtests)?
- Should go-fsfix know about a base directory at all, or should Endless's test helpers wrap it?
