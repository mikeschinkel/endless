# E-1569 — Add worktree.bgIsolation:'none' to just claude-settings-init output

Lands after E-1571 (no DB dependency, but sequencing per epic plan) and **before** E-1568 (--bg dispatch is only usable once bgIsolation is set in each worktree's settings.json). Small Justfile/Python change.

## Decisions

- **Setting location.** `worktree.bgIsolation: "none"` at the top level of `.claude/settings.json`. (Per the v2.1.143+ Claude schema documented in `docs/research-2026-06-12-claude-background-agents.md` §3.)
- **Where to inject.** Inside the inline `python3 -` script in `Justfile`'s `claude-settings-init` recipe. After the `out["hooks"] = ...` line (which finalizes the assembled dict), unconditionally add:
  ```python
  out["worktree"] = {"bgIsolation": "none"}
  ```
  Any pre-existing `worktree` key from `working` (sandbox-bind doesn't write one today) is overwritten. If a future migration starts setting `worktree.<other>`, merge rather than replace — but for this child, plain assignment is correct because no other code populates the key.
- **Idempotence preserved.** Re-running `just claude-settings-init` writes the same key with the same value. Existing `--skip-worktree` mark means git status stays clean.
- **No fallback for older Claude.** Mike is on a recent Claude. If a downgrade is ever needed, the key is benign (older Claude ignores unknown settings) — no version gating in the recipe.
- **No flag.** Not opt-out. bgIsolation:'none' is a hard requirement for the bg-dispatch system this epic builds. Surfacing it as a flag would let a future Mike-or-agent disable the wiring and silently break dispatch.

## Files

### Changed
- `Justfile` — one-line addition inside the embedded Python script (before `with open(out_path, "w") as f:`). Update the recipe's leading comment block to mention bgIsolation and link to the rationale ("Required by E-1564 epic; prevents Claude from creating a nested `.claude/worktrees/` under endless's tracker worktree").

### Tests
- `tests/test_claude_settings_init.py` (new, or extend existing if there's already coverage — grep `test_claude_settings`): write a fake `~/.claude/settings.json` and a fake committed/working `.claude/settings.json`, run the recipe via subprocess, parse output, assert `out["worktree"]["bgIsolation"] == "none"`. Two cases: fresh (no prior worktree key) and re-run (idempotent).

## Sequence note

Required by **E-1568**. Independent of E-1565/E-1566 contentwise but sequenced after them per the epic's overall ordering.

## Verification

1. From a worktree, run `just claude-settings-init` and `grep bgIsolation .claude/settings.json` → expect `"bgIsolation": "none"`.
2. `cat .claude/settings.json | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d["worktree"]["bgIsolation"]=="none"'`.
3. Re-run; confirm output file is byte-identical (no churn).
4. From the main checkout, `just claude-settings-init` still refuses with the existing error message (regression check).
5. `just test` passes (including the new test).
