REVISED 2026-08-21 (E-1944). The original text was written against endless-event, a binary that no longer exists: E-1367 consolidated the endless-* binaries into a single endless-go with subcommands, so the surface is now 'endless-go event ...' and the only installed symlink is endless-go, pointing into the MAIN checkout's bin/. The original premise -- 'just install from worktree A makes every command hit A's binary' -- is also no longer the live failure: CLAUDE.md now requires just install to run from the main checkout only, so the symlink deliberately points at main.

SCOPE OF THE REMAINING GAP. The worktree binary is already preferred where it has been wired explicitly: E-998 writes an absolute path to <worktree>/bin/endless-go into the worktree's .claude/settings.json, so hooks run candidate code, and invoking ./bin/endless-go directly obviously does too. The gap is NAME-RESOLVED invocation -- the Python CLI dispatching to the Go binary, and anything found via PATH -- which still reaches main's binary from inside a worktree.

RAISED IMPORTANCE. Under ED-1567 and ED-1570 the binary that runs determines which schema version is expected, and a mismatch now HALTS rather than merely behaving stalely. Picking the wrong binary stops being a staleness annoyance and becomes a hard stop.

## From the description

Approaches: (a) prepend ./bin to PATH in shell-init when in a worktree, (b) have the Python CLI auto-detect a worktree-local bin/ and prefer it, mirroring E-1368's cwd self-detection, (c) install per-worktree to alternative paths.
