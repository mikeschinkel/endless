E-787 Phase 5 manual sanity uses bash 'unset XDG_CONFIG_HOME' and 'rm -rf .test-isolated' for cleanup.

Easy to forget the unset and leave the shell pointing at a deleted path, leading to confusing errors on the next endless command.

Pytest fixtures have no such issue (monkeypatch auto-cleans), but binary-level/manual sanity flows are exposed.
