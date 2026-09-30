4KB limit was based on PIPE_BUF atomicity which only applies to pipes, not regular files. Local filesystem O_APPEND is atomic regardless of size.
