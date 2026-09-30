Currently src/endless/config.py hardcodes Path.home() / '.config' / 'endless'. The Go side (internal/monitor/db.go) honors XDG_CONFIG_HOME via os.Getenv; Python doesn't.

This parity gap blocks isolated binary-level testing of Python CLI commands — the pytest isolated_env fixture works in-process via monkeypatch but cannot isolate subprocess-launched binaries.
