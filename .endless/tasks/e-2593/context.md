Tests isolate Endless by pointing HOME, XDG_CONFIG_HOME and --db-dir at a temp directory (E-1043 designs the one fixture for that). The code under test still sees the host's real filesystem layout: a fixture path is /private/var/folders/... or /tmp/..., never /, and rules written in absolute paths (E-2369) see the host, not the fixture.

Mike, 2026-11-09: a lighter-weight, directory-only isolation, for when the heavier isolation (containers, VMs: E-2450) is not needed. File it for Linux.
