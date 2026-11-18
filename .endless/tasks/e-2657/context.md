E-2572 found that the tmux command lines Endless generates do not carry the server they belong to, apart from `record-windows`. `tmux status-line`, `show-menu`, `monitor-wake` and `spawn-launch` act on whichever server `$TMUX` names, and `Server.Init` fakes `$TMUX` to aim `tmux init` at a server.

Mike, planning E-2572: each command that tmux runs inside an Endless server receives its server's socket explicitly, on command lines Endless generates per server. He wants separate lines per server, because the console is to have its own status line (E-2652, which waits for this task).
