The spawn handoff templates open with 'Your spawning session's task is: E-N — return to that session with tmux switch-client -t <anchor>' and close with a prominent 'tmux move-window … && switch-client -t <anchor>' return line.

Both predate session monitor/goto and are now noise: session monitor shows the spawning task and session goto returns to it.
