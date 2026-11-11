Every rule path is read relative to the base directory: /private becomes <base>/private, /etc becomes <base>/etc. A test builds a fake root under its temp dir (creating whatever symlinks it needs, e.g. <base>/tmp -> private/tmp) and runs the real built-in rules against it, so the fixtures' `remove` override can go.

Today the flag is global on endless-go and Python passes it through, the way --db-dir is. After E-1063 there is one Go binary: it is a global flag on the root command, parsed once and handed to the resolver, with no pass-through at all.

Windows known-folder lookups ask the OS and cannot be relocated; Windows is not supported yet, so that waits.

Where the rules are consulted: monitor.ResolveDirectory, reached from ProjectIDForPath (hook auto-registration, prompt and session-query lookups), ProjectForCwd, and the `project resolve` verb Python uses for register, discover, project_sync, list, unregister and project_name_for_cwd.

## Decisions (Mike, 2026-11-10 and 2026-11-11)

- A directory outside the base directory is an error naming both paths, not a pass with no rules and not a check against the host's rules. Inside a test, a stray directory is a fixture bug and should fail loudly.
- The flag is hidden from the human --help and listed in the agent-facing help, and the disallowed-paths guide page documents it.
- Flag only, no environment variable: a process is rooted only when told explicitly, so nothing inherits a fake root by accident. No test drives a real Claude session's hook today; a variable can be added if one ever needs it.
- The fixtures move to a per-test base directory in this task. Go hands every t.TempDir() call in one test a subdirectory of a single per-test directory, so that directory can be the base without restructuring tests; subtests, and helpers using os.MkdirTemp or fixed paths, need individual attention.
- Rule 1 reads HOME as set, unprefixed, so it agrees with tilde expansion and the config-dir lookup. When the base directory is not /, a HOME outside it is an error, so a rooted test can never apply the home rule to the real home. Keeping HOME itself away from the live environment (a nonexistent default HOME and a home fixture for tests that need one) is E-2594's job, not this task's.

## Decided without asking

- Paths in the overrides file are read relative to the base directory too; under base / that is today's meaning.
- Rule 1 refuses HOME's ancestors only up to the base directory, not host directories above it.
- The resolver does not reinterpret absolute symlink targets; a fake root uses relative links (macOS's own /tmp, /var and /etc links already are).
- The base directory is symlink-resolved before comparison, like every other path the resolver compares.
- Go unit tests set the base directory through a test seam in internal/monitor/test_seam.go rather than a flag.
- One OS still checks only its own rule set: --base-dir relocates the rules, it does not let a macOS run exercise the Linux list.
