Test runs inherit the developer's whole environment. tests/conftest.py's isolated_env keeps a block list: it sets HOME, XDG_CONFIG_HOME and CLAUDE_CONFIG_DIR and removes TMUX, CLAUDECODE and a dozen more, so anything it does not name reaches the tests: AWS_*, GITHUB_TOKEN, ANTHROPIC_API_KEY, SSH_AUTH_SOCK. Go tests get no clearing at all.

Mike, 2026-11-10: make absolutely sure HOME never accidentally points at a live environment; tests should clear the environment and add only what they need, so nothing like an AWS token that can delete all of S3 is ever reachable by accident. Locking down must still leave a way to open access when a test genuinely needs it. A test that needs a home directory should get its own home fixture.

Other ways a test reaches live credentials today:
- Python's Path.home() falls back to the password database (the real home) when HOME is unset; Go's os.UserHomeDir() returns an error instead.
- Apple's git ships a system gitconfig believed to set credential.helper=osxkeychain; ~/.gitconfig supplies identity, signing and hooks.
- src/endless/internal_claude.py's run_internal_claude runs the real `claude -p`, which authenticates through the macOS keychain. Six modules call it (rater, plan_challenge, report_cmd, minimizer_judge, minimizer_optimizer, task_cmd); their tests stub it today, but nothing stops one that forgets.

No test touches a network remote: tests/test_ownership.py sets GitHub URLs only for Endless to parse.

This guards against accidents, not intent: a test written to read ~/.ssh by absolute path or query the keychain directly is the macOS sandbox task's concern.
