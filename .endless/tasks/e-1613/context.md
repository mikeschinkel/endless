_resolve_endless_go() falls back to shutil.which('endless-go') (the global install), so callers can silently exercise a stale binary lacking new subcommands.
