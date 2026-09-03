"""E-2105: no command pays for the session state vocabulary unless it uses it.

`endless.statuses` reads its vocabulary at import, because `click.Choice(
TASK_STATUSES)` in cli.py is evaluated as the decorators run. That is safe for a
subcommand every installed `endless-go` already has, and fatal for one being
introduced — which is what `session-state` was.

`just land` fast-forwards main's Python source into place and rebuilds the
global binary only at the END of the recipe. In between, every `endless`
invocation from the main checkout runs NEW Python against an OLD binary, and
`config.worktree_endless_go()` answers None there, so the stale global is the
only candidate. The first land of E-2105 died in that window: `endless worktree
land` — a command with nothing to say about session state — exited at import
because cli.py had asked the registry a question no binary on the machine could
yet answer. No resolver could have rescued it; the fix is not to ask.

These pin the two halves of that: the lookup is deferred, and constructing the
click type does not perform it.
"""

import click
import pytest

from endless import session_states


class _Recorder:
    """Stands in for `session_states.get`, recording every group asked for."""

    def __init__(self, states=("working", "idle", "needs_input", "ended")):
        self.calls: list[str] = []
        self._states = states

    def __call__(self, group: str) -> tuple[str, ...]:
        self.calls.append(group)
        return self._states


def test_constructing_the_choice_asks_the_registry_nothing(monkeypatch):
    """The decorator runs at import; it must not shell out.

    This is the assertion that would have caught the failed land. If
    constructing StateChoice consults the registry, the lookup is back at
    import time no matter how the accessor is spelled.
    """
    rec = _Recorder()
    monkeypatch.setattr(session_states, "get", rec)

    session_states.StateChoice()

    assert rec.calls == []


def test_converting_a_value_asks_the_registry(monkeypatch):
    """Deferred, not skipped: the option is still validated against the
    vocabulary, and against the live one rather than a copy."""
    rec = _Recorder()
    monkeypatch.setattr(session_states, "get", rec)

    assert session_states.StateChoice().convert("needs_input", None, None) == "needs_input"
    assert rec.calls == ["all"]


def test_a_value_outside_the_vocabulary_is_refused_naming_it(monkeypatch):
    """The rejection is click.Choice's own — delegated, not re-implemented, so
    the message cannot drift from what every other Choice-typed option says."""
    monkeypatch.setattr(session_states, "get", _Recorder())

    with pytest.raises(click.exceptions.UsageError) as exc:
        session_states.StateChoice().convert("prompted", None, None)

    message = str(exc.value)
    assert "'prompted' is not one of" in message
    for state in ("working", "idle", "needs_input", "ended"):
        assert repr(state) in message


def test_the_metavar_is_the_vocabulary_and_is_also_deferred(monkeypatch):
    """`--state [working|idle|needs_input|ended]` in --help comes from the
    registry too, so a state added in Go documents itself."""
    rec = _Recorder()
    monkeypatch.setattr(session_states, "get", rec)

    param = click.Option(["--state"], type=session_states.StateChoice())
    assert rec.calls == []

    metavar = param.type.get_metavar(param, click.Context(click.Command("x")))
    assert metavar == "[working|idle|needs_input|ended]"


def test_cli_imports_without_the_registry_answering(monkeypatch):
    """The whole point, stated end to end.

    `endless.cli` is already imported by the time this runs, so reloading it is
    the only way to observe its import. Reload is safe here: the module defines
    a click group and its commands, and rebinding them is exactly what a fresh
    process does.
    """
    import importlib

    import endless.cli

    def refuse(*args, **kwargs):
        raise AssertionError(
            "cli.py consulted the session state registry at import time — "
            "that is the dependency E-2105 removed; see this module's docstring"
        )

    monkeypatch.setattr(session_states, "_run", refuse)
    monkeypatch.setattr(session_states, "get", refuse)

    importlib.reload(endless.cli)
