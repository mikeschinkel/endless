The Python endless CLI is installed via 'uv tool install' but not in editable mode — it copies src/endless/ into the uv tool's site-packages, so after every merge to main the installed copy is stale until 'uv tool install --reinstall .' runs.

We hit this immediately after landing E-990: 'endless session cd' was missing on PATH despite being merged.
