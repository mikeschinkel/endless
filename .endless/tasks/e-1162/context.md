endless-sandbox enter/run exports ENDLESS_SANDBOX=<sandbox-dir>;

inside that subshell every endless command that does I/O is wrong-by-construction (DB, config, verbs.json all redirected by XDG_CONFIG_HOME).

Discovered 2026-05-03 verifying E-1114.
