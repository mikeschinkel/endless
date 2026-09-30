gotest and pytest become drivers.

Add the pytest/uv registered variant with contract-defined launcher resolution (prefer project venv plain pytest executable that survives HOME-isolation, then uv run pytest, then bare pytest); bare pytest keeps documented fallback precedence.

A generic driver covers raw/shell/arbitrary-tool runners: exec the declared command, require a normalizable result stream (TAP/CTRF).
