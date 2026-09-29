This is a hard violation of the surface-all-errors gating principle: a failed migration step silently returns nil success, and downstream code runs against a half-migrated schema.

Surfaced during E-1322 review; not E-1322's scope to fix because it's a cross-version cleanup.
