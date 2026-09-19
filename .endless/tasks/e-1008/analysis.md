The Python endless CLI is installed via 'uv tool install' but not in editable mode — it copies src/endless/ into the uv tool's site-packages. After every merge to main, the installed copy is stale until 'uv tool install --reinstall .' runs. We hit this immediately after landing E-990: 'endless session cd' was missing on PATH despite being merged.

'just install' only handles Go binaries (just build + symlink). The Python tool install is manual and easy to forget.

Two routes:

1. Editable install: 'uv tool install --editable .' (verify uv supports --editable for tool installs, or install via 'uv pip install -e .' into a tool venv). Source changes become live without any post-merge step. Cleanest if uv supports it cleanly.

2. Add 'uv tool install --reinstall .' to the 'just install' recipe so a single 'just install' refreshes both Go and Python sides. Costs are: every install rebuilds Python deps (small but non-zero), and you still have to remember to run 'just install' after merges.

Recommend #1 if it works; fall back to #2.

Verification:
- After merging a Python source change to main, 'endless --version' reports the new version (or new behavior is live) without manually reinstalling.
- 'just install' is the single command for refreshing the local toolchain.

Origin: E-990 landing exposed the gap.