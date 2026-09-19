# E-1528: Clear ENDLESS_SESSION_ID in test_session_resolver.py

## Context

8 tests in `tests/test_session_resolver.py` fail when `ENDLESS_SESSION_ID` is exported in the runner's environment — standard for any Endless-spawned Claude session, so the failure pattern affects every dev session that runs `just test` without first stripping the var.

Cause: `_resolve_session_id_with_prompt` (`src/endless/task_cmd.py:2052`) starts with a Layer 1 env-var read; if `ENDLESS_SESSION_ID` is set, the function returns that value immediately and never consults the test-mocked pane data. So every test in that file either gets the env id back (e.g. `assert 530 == 42`), or its mocked `click.prompt` is never invoked (`KeyError: 'question'`), or its expected `ClickException` doesn't raise.

Empirically scoped: `ENDLESS_SESSION_ID=530 uv run pytest tests/` produces failures *only* in `tests/test_session_resolver.py`. No other test file exercises an env-bled session-id path.

## Decision

Add a module-scope `autouse` fixture in `tests/test_session_resolver.py` that strips `ENDLESS_SESSION_ID` for every test in that file. Localized to the file rather than `tests/conftest.py` so future tests that legitimately depend on the env var (or want to verify Layer 1's behavior) aren't masked by a blanket-clear at conftest scope.

## Implementation

Add at the top of `tests/test_session_resolver.py`, after the existing imports (`pytest` is already imported):

```python
@pytest.fixture(autouse=True)
def _clear_endless_session_id(monkeypatch):
    """Clear ENDLESS_SESSION_ID so each test exercises the resolver's
    pane/session-table layers, not its Layer 1 env-var short-circuit
    (task_cmd.py:2052)."""
    monkeypatch.delenv("ENDLESS_SESSION_ID", raising=False)
```

`monkeypatch` (pytest builtin) handles teardown automatically — no manual reset needed.

## Verification

1. `ENDLESS_SESSION_ID=530 uv run pytest tests/test_session_resolver.py -v` — all 11 tests pass.
2. `env -u ENDLESS_SESSION_ID uv run pytest tests/test_session_resolver.py -v` — all 11 tests still pass (no regression in the env-unset case the file was originally written against).
3. `just test` from a shell with `ENDLESS_SESSION_ID` exported — full suite green (the dev-session failure mode that prompted the task).

## Coordination

After landing, sweep `tests/` for any other files that call `_resolve_session_id_with_prompt`, `_resolve_session_id_required` (`task_cmd.py:1977`), or similar session-id readers, to confirm no other files have latent env-bleed. Empirical baseline today: only this file's tests fail under `ENDLESS_SESSION_ID=N`, so no broader fan-out is needed unless that changes.
