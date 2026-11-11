Proposed shape (from the E-2592 planning discussion, 2026-11-10):
- just test and just test-go launch through `env -i` with an allowlist (PATH, LANG, TMPDIR, a fake HOME, fixed git identity, a marker variable). conftest.py and a shared Go test helper refuse to run without the marker, so a bare pytest or go test cannot skip the clearing.
- HOME defaults to a path that does not exist inside the test's temp tree; a home fixture builds a real fake home for tests that need one. First step: measure how many tests assume isolated_env's home exists.
- GIT_CONFIG_NOSYSTEM=1 and GIT_CONFIG_GLOBAL=/dev/null, with GIT_AUTHOR_*/GIT_COMMITTER_* set to fixed test values.
- Under the marker, run_internal_claude refuses to launch the real claude unless the test stubs it.
- Opening access per run, never by inheritance: a live_claude mark plus a dedicated token (ANTHROPIC_API_KEY or CLAUDE_CODE_OAUTH_TOKEN from `claude setup-token`) passed only by a `just test-live` recipe; a live_network mark likewise.
- Verify steps run under the same cleared environment; a step that needs Claude or the network declares it and gets only that. Past verify scripts are not audited (Mike, 2026-11-11: only future capability matters).
- A check in just test flags new test code naming security, osxkeychain, SSH_AUTH_SOCK, /Users/ or network hosts.

This would fulfil E-1043's goal (in-process Python and subprocess binaries see the same isolated paths) as a side effect.
