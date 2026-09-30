Files: config.go (EndlessConfig struct + RootConfig() marker), merge.go (per-field Merge() with semantics from the plan: per-key checks merge, layerable tracking, etc.), normalize.go (defaults; move defaultCheckEnabled from internal/monitor/checks.go here), errors.go (doterr sentinels), load.go (Load(projectPath) wrapping cfgstore.LoadDefaultConfig[EndlessConfig, *EndlessConfig] with ConfigSlug=endless, ConfigFile=config.json).

Struct must include EVERY current field from both ~/.config/endless/config.json (roots, scan_interval, ignore, ownership, node_id, checks) and per-project .endless/config.json (name, label, description, language, status, dependencies, documents, tracking) with JSON tags identical to today so existing files keep loading.

Use go-doterr for all errors and go-dt for paths.
