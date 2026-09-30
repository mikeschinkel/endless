tasks.description is a short single-line metadata field, so a --description-file loader (added alongside the other --xxx-file flags) has no real use — you would never load a one-line summary from a file.

Coordinate with the inline-flag path gate: if this flag is removed, the gate's redirect for a path mis-passed to --description can no longer say 'use --description-file' and should instead say descriptions are short inline text.

The loader also never stripped a trailing newline, so any file written by echo, a heredoc or an editor tripped the one-line rule; removing the flag removes that trap with it.
