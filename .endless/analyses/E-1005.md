Observed during E-970 implementation (2026-04-29). When multiple Claude Code sessions are running endless work concurrently, the editable uv tool install at ~/.local/share/uv/tools/endless/ keeps getting reverted to a snapshot install. The marker is the absence of _endless.pth; when present, edits to src/endless/*.py take effect immediately, when absent, only the snapshot version runs.

Symptom: 'endless phrase add foo' succeeds in one terminal, then immediately running 'endless task add "Foo bar"' fails because validate_title is using a stale snapshot that doesn't have the new matchers code.

Workaround: 'uv tool install -e . --force' restores editable mode; needs to be re-run periodically.

Suspected cause: another session is running 'uv tool install' (without -e) somewhere, possibly as part of a build script or a setup hook.

Investigation steps:
- Grep for 'uv tool install' in Justfile, hooks, and any other CI-ish scripts; identify which paths use -e.
- Check whether 'just install' (Justfile recipe) preserves -e or breaks it.
- Determine whether endless has a self-install mechanism that runs and overwrites.

Resolution options:
- Standardize on 'uv tool install -e .' everywhere
- Make the editable install permanent via a different mechanism (symlink site-packages?)
- Document and use an alias that always reinstalls editable

Cost: this issue cost ~30 minutes of wasted debugging during E-970 testing.