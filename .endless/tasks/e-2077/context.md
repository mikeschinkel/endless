Per-task verify scripts live in tests/tasks/*.sh, which makes them look like a test suite.

They are not one: each drives real binaries (hook, endless-go, git, tmux) and several are not isolated from the live environment.

Running e-1202-verify writes a session row into the MAIN database bound to the caller's actual tmux pane, after which two sessions claim one pane and every companion-resolving command, esu included, refuses to pick between them.
