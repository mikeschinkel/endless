When a registered migration sets RequiresRebuild: true, internal/monitor/migrate.go returns ErrRequiresRebuild and refuses to advance the schema unless opts.AllowRebuild is set.

Discovered during E-1396 when adding the flag on V11 broke 53 fresh-DB tests immediately, forcing me to drop the flag and rely on BEGIN IMMEDIATE TRANSACTION alone.

The flag is currently unused on main but the next user who needs it will hit the same wall — and worse, can't ship it at all without breaking every fresh install.
