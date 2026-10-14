`TestTmuxTestsUsePrivate` (internal/tmuxtest, from E-2310) fails on main. It
flags `internal/monitorvis/e2e_test.go` (from E-2229), whose `newVisServer`
starts tmux with `-S <dir>/s`.

That test is already isolated: `<dir>` comes from `os.MkdirTemp("/tmp",
"e2229-")`, the server gets its own HOME and has TMUX and TMUX_PANE stripped,
and cleanup runs kill-server and removes the directory. It just never calls
`tmuxtest.Private`.

The guard works per file: any `_test.go` that contains the string literals
`"tmux"` and `-L` or `-S` but never calls `tmuxtest.Private` is flagged,
whether or not its socket is already private. The two tasks landed separately,
so neither ran against the other.

To find the site, run `go test ./internal/tmuxtest/ -run TestTmuxTestsUsePrivate`.
