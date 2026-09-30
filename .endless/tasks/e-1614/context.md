Advisory/best-effort code that swallows its own failures (e.g. _bg_throttle_warn) makes 'correctly did nothing' indistinguishable from 'silently broke', which lets verify suites report false greens.
