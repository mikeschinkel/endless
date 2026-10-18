Under `--db sandbox`, Python reads and writes the sandbox's `config.json` (`<worktree>/.endless/sandbox/endless/config.json`; config.py rebinds CONFIG_FILE). Go does not: `config.Load` takes its user layer from go-cfgstore's CLI directory, which is $XDG_CONFIG_HOME, else $HOME/.config, whatever the DB context. So the two halves of one command can read different config files, and a setting changed in the sandbox to test a feature reaches Python and not Go.

For tmux this means anything Endless does under `--db sandbox` (liveness observation, spawns, the console and tasks servers) uses the user's real socket directory and reaches the user's live Endless tmux servers. No test or sandboxed exercise needs those servers. The session doing the work runs on them, but its hooks are pinned to main and are unaffected by any of this.

Config is not the only route in. Liveness's observeTmuxServers first asks "the tmux this process is inside" (`tmux display-message -p #{socket_path}`, via $TMUX). So a sandboxed command run from the user's pane observes their real server whatever `socket_dir` says.

The verify runner already isolates its runs. It strips TMUX and TMUX_PANE and gives each run a private TMUX_TMPDIR under /tmp (verifycmd callerIdentityVars and tmuxCommand). It also swaps in a temp HOME. So this task concerns `--db sandbox` use from a real session, not verify runs.

This was first filed with a wrong cause, "a worktree ignores tmux.socket_dir", found while writing E-2300's verify suite. The real cause of that symptom is go-cfgstore's layer merge, which discards the merged result whenever a project layer exists, in any directory. That is fixed separately in go-cfgstore.
