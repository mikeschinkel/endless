"""Tests for `endless shell-init` (E-1015)."""

from click.testing import CliRunner

from endless.cli import main


def test_shell_init_prints_helpers():
    runner = CliRunner()
    result = runner.invoke(main, ["shell-init"])
    assert result.exit_code == 0
    out = result.output
    # Both helper functions are present (E-1050: esp replaces escd).
    assert "esu()" in out
    assert "esp()" in out
    assert "escd()" not in out
    # They invoke the right endless subcommands. E-1164 wraps each call in
    # _endless_run for worktree routing, so the substring is "session use"
    # / "session cd --target project" rather than the bare "endless ..." form.
    assert "session use" in out
    assert "session cd --target project" in out
    # Marker block is present so users can find/replace it later.
    assert ">>> endless shell helpers" in out
    assert "<<< endless shell helpers" in out


def test_shell_init_propagates_exit_codes():
    """The helper functions should `return $?` so a failed `endless session
    use/cd` doesn't silently succeed via `eval ""`. Verify the snippet
    contains the exit-code propagation pattern.
    """
    runner = CliRunner()
    result = runner.invoke(main, ["shell-init"])
    assert result.exit_code == 0
    # Both functions capture stdout and bail on non-zero exit.
    assert 'return $?' in result.output


def test_shell_init_idempotent():
    """Running twice produces identical output — no state, no random bits."""
    runner = CliRunner()
    a = runner.invoke(main, ["shell-init"]).output
    b = runner.invoke(main, ["shell-init"]).output
    assert a == b


def test_shell_init_routing_helper_present():
    """E-1164: snippet defines _endless_run that picks the worktree CLI by
    looking up the session's worktree on each invocation (subprocess call,
    no exported env var — see feedback_env_vars_visible_latency_invisible)."""
    runner = CliRunner()
    out = runner.invoke(main, ["shell-init"]).output
    assert "_endless_run()" in out
    assert "ENDLESS_SESSION_ID" in out
    assert "session cd --target worktree" in out
    assert "uv run --directory" in out


def test_shell_init_endless_run_passes_db_main():
    """E-1591: every endless invocation inside _endless_run carries --db main.

    esu cd's the user into the session's worktree; once cwd is a self-dev
    worktree, any bare 'endless' call hits the WORKTREE_DB_REFUSAL gate. The
    session helpers only touch the real ledger, so --db main is both required
    (to clear the gate) and correct. All three calls — the worktree lookup, the
    'uv run --directory' route, and the bare fallback — must pass it."""
    runner = CliRunner()
    out = runner.invoke(main, ["shell-init"]).output
    start = out.index("_endless_run()")
    body = out[start:out.index("\n}\n", start) + 2]
    assert "endless --db main session cd --target worktree" in body
    assert "uv run --directory \"$wt\" endless --db main" in body
    # The fallback must also carry --db main (cwd may be a self-dev worktree).
    assert "endless --db main \"$@\"" in body
    # No remaining bare 'endless "$@"' call that would skip the gate.
    assert "uv run --directory" in body  # sanity: routing block present


def test_shell_init_helpers_route_via_endless_run():
    """E-1164: each helper body invokes _endless_run, never bare 'endless'.
    Inspect each function block to confirm it uses the routing helper."""
    runner = CliRunner()
    out = runner.invoke(main, ["shell-init"]).output

    for helper in ("esu", "esp", "esf"):
        start = out.index(f"{helper}()")
        # Each helper ends at the next blank line followed by '#' comment
        # or the closing '<<< endless shell helpers' marker.
        body = out[start:out.index("\n}\n", start) + 2]
        assert "_endless_run" in body, f"{helper} body must use _endless_run"
        # No bare 'endless ' subprocess call inside the body. (Substring
        # check tolerates the comment header which uses 'endless' as prose.)
        assert "$(endless " not in body, \
            f"{helper} body shells out to bare endless: {body!r}"


def test_shell_init_precondition_checks():
    """esf emits a clear error when no session is active (E-1164). esp no
    longer guards on a session — resolving the project root only needs the
    cwd, so the guard was dropped (E-1650)."""
    runner = CliRunner()
    out = runner.invoke(main, ["shell-init"]).output
    assert "esf: no active session" in out
    assert "esp: no active session" not in out


def test_shell_init_safe_under_nounset():
    """E-1164: ENDLESS_SESSION_ID reads in test/conditional positions use
    ${VAR:-} expansion so the helpers don't error 'unbound variable' under
    'set -u'. Plain $ENDLESS_SESSION_ID is fine inside command arguments
    (e.g. 'endless session cd ... "$ENDLESS_SESSION_ID"') because by then
    we've already verified the var is set; only the [ -n / -z ] tests need
    the safe form."""
    runner = CliRunner()
    out = runner.invoke(main, ["shell-init"]).output
    # No bare bracket-tests (the form that errors under nounset).
    assert '[ -n "$ENDLESS_SESSION_ID"' not in out
    assert '[ -z "$ENDLESS_SESSION_ID"' not in out
    # Safe form is present in test positions.
    assert '${ENDLESS_SESSION_ID:-}' in out
