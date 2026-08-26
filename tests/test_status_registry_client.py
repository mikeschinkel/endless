"""The Python side of the status registry (E-1891).

`endless.statuses` is a pass-through client for `endless-go task-status`. These
tests pin the two properties that make it a client rather than a second
registry: every answer comes from the Go command, and the module holds no
vocabulary or group list of its own. Plus the failure mode, which the plan asked
be decided explicitly rather than discovered: it fails closed, loudly, naming
what to do about it.

The vocabulary's CONTENT is not asserted here — that lives in the Go package's
tests, which is the whole point. What is asserted is that Python reports
whatever Go said.
"""

import subprocess

import pytest

from endless import config, statuses


def _go(*args: str) -> subprocess.CompletedProcess:
    """Invoke the same binary `statuses` would, bypassing the wrappers."""
    return subprocess.run(
        [statuses._binary(), "task-status", *args],
        capture_output=True, text=True,
    )


# --- every wrapper returns what the Go command emitted ---------------------


def test_get_matches_the_go_command_for_every_group():
    """Walked over the registry, not a hand-listed set: a group added in Go is
    covered here the moment it exists, with no change to this file."""
    for group in statuses.groups():
        assert statuses.get(group) == tuple(_go("get", group).stdout.split()), group


def test_groups_matches_the_go_command():
    assert statuses.groups() == tuple(_go("groups").stdout.split())


def test_task_statuses_is_the_all_group():
    assert statuses.TASK_STATUSES == statuses.get("all")


def test_status_help_is_derived_from_the_vocabulary():
    """A status added in Go must show up in every `--help` for free — the
    failure E-1956 fixed, now guaranteed one level further down."""
    for status in statuses.TASK_STATUSES:
        assert status in statuses.TASK_STATUS_HELP


def test_has_matches_the_go_command_exit_code():
    for group in statuses.groups():
        members = statuses.get(group)
        for status in statuses.TASK_STATUSES:
            assert statuses.has(group, status) is (status in members), (group, status)


def test_sql_list_matches_the_go_command():
    for group in statuses.groups():
        assert statuses.sql_list(group) == _go("sql-list", group).stdout.strip()


def test_sql_list_is_a_usable_in_clause():
    """The rendered form has to survive being spliced into SQL, which is the
    only reason the accessor exists."""
    from endless import db
    rendered = statuses.sql_list("terminal")
    rows = db.query(f"SELECT 1 WHERE 'confirmed' IN ({rendered})")
    assert rows, rendered
    assert not db.query(f"SELECT 1 WHERE 'ready' IN ({rendered})")


def test_rank_matches_the_go_command():
    for status in statuses.TASK_STATUSES:
        assert statuses.rank("derivation-precedence", status) == int(
            _go("rank", "derivation-precedence", status).stdout
        )


def test_label_and_glyph_match_the_go_command():
    for status in statuses.TASK_STATUSES:
        assert statuses.label(status) == _go("label", status).stdout.strip()
        assert statuses.glyph(status) == _go("glyph", status).stdout.strip()


# --- the module holds no knowledge of its own -----------------------------


def test_module_source_carries_no_status_or_group_literals():
    """The structural claim: this file knows verb names and nothing else. A
    status slug or a group name appearing in the source would be the start of
    the second registry E-1891 exists to prevent — and, unlike a stale copy, a
    test can see this one.

    Two exemptions, both narrow. Docstrings are prose about the design and
    legitimately name statuses. And `"all"` appears once, as the argument to the
    single import-time call: cli.py's `click.Choice(...)` is evaluated at
    decoration time, so the whole vocabulary has to be fetched as the module
    loads. That is a verb argument, not a group registry — the difference this
    test enforces is that ONE group name may be named, never a list of them.
    """
    import ast
    import inspect

    source = inspect.getsource(statuses)
    tree = ast.parse(source)
    docstrings = {ast.get_docstring(n) for n in ast.walk(tree)
                  if isinstance(n, (ast.Module, ast.ClassDef, ast.FunctionDef))}
    literals = [
        node.value for node in ast.walk(tree)
        if isinstance(node, ast.Constant) and isinstance(node.value, str)
        and node.value not in docstrings
    ]

    named_statuses = set(literals) & set(statuses.TASK_STATUSES)
    assert not named_statuses, (
        f"endless/statuses.py names the statuses {sorted(named_statuses)} "
        "literally; it must ask endless-go instead"
    )

    named_groups = [lit for lit in literals if lit in set(statuses.groups())]
    assert named_groups == ["all"], (
        f"endless/statuses.py names the groups {sorted(set(named_groups))}; "
        'only the import-time "all" fetch may name one'
    )


# --- failure is closed, and says what to do -------------------------------


def test_unknown_group_raises_rather_than_answering_empty(monkeypatch):
    with pytest.raises(statuses.StatusVocabularyError) as exc:
        statuses.get("no-such-group")
    assert "no-such-group" in str(exc.value)


def test_unknown_status_raises(monkeypatch):
    with pytest.raises(statuses.StatusVocabularyError):
        statuses.label("in_progress")


def test_missing_endless_go_fails_closed_with_an_actionable_message(monkeypatch):
    """The decision E-1891's plan asked to be made explicitly: there is no
    fallback to fall back TO — a hardcoded Python copy would be exactly the
    duplicate this module deletes — so it says so and stops."""
    monkeypatch.setattr(config, "worktree_endless_go", lambda *a, **k: None)
    monkeypatch.setattr(statuses.shutil, "which", lambda _: None)
    monkeypatch.setattr(statuses, "_BINARY", None)
    with pytest.raises(statuses.StatusVocabularyError) as exc:
        statuses.get("all")
    message = str(exc.value)
    assert "endless-go" in message
    assert "just install" in message


def test_stale_endless_go_names_the_command_that_failed(monkeypatch, tmp_path):
    """An endless-go predating `task-status` is the realistic failure — the two
    binaries ship together but can be installed apart. The error has to name the
    exact invocation, or the operator is guessing."""
    fake = tmp_path / "endless-go"
    fake.write_text('#!/bin/sh\necho \'endless-go: unknown subcommand "task-status"\' >&2\nexit 2\n')
    fake.chmod(0o755)
    monkeypatch.setattr(config, "worktree_endless_go", lambda *a, **k: None)
    monkeypatch.setattr(statuses.shutil, "which", lambda _: str(fake))
    monkeypatch.setattr(statuses, "_BINARY", None)
    with pytest.raises(statuses.StatusVocabularyError) as exc:
        statuses.get("all")
    message = str(exc.value)
    assert "task-status get all" in message
    assert "unknown subcommand" in message
    assert "just install" in message


# --- binary resolution -----------------------------------------------------


def test_prefers_the_worktree_binary_over_path(monkeypatch, tmp_path):
    """A self-dev worktree runs the worktree's Python; the worktree's Go is its
    coherent partner. Without this the vocabulary would come from main's build
    while branch source may have changed it — and the branch that ADDS
    `task-status` could not run at all before landing."""
    worktree_bin = tmp_path / "endless-go"
    worktree_bin.write_text("#!/bin/sh\nexit 0\n")
    worktree_bin.chmod(0o755)
    monkeypatch.setattr(config, "worktree_endless_go", lambda *a, **k: worktree_bin)
    monkeypatch.setattr(statuses, "_BINARY", None)
    monkeypatch.setattr(statuses.shutil, "which", lambda _: "/usr/local/bin/endless-go")
    assert statuses._resolve_binary() == str(worktree_bin)


def test_falls_back_to_path_when_the_worktree_binary_is_too_old(monkeypatch, tmp_path):
    """The regression that made E-1891 briefly unlandable-in-practice.

    A worktree branched BEFORE `task-status` landed has a bin/endless-go that
    cannot answer, and rebuilding it does not help — that branch has no
    `task-status` code to build. Preferring it anyway made every `endless`
    command inside such a worktree fail fatally, including `--help`.

    It bit worse than it looks because cli.py imports this module at line 15 but
    only re-execs into the worktree's OWN Python much later: main's Python asked
    the stale worktree binary before it could hand off to the branch's Python,
    which does not need the subcommand at all.
    """
    stale = tmp_path / "endless-go"
    stale.write_text(
        '#!/bin/sh\necho \'endless-go: unknown subcommand "task-status"\' >&2\nexit 2\n'
    )
    stale.chmod(0o755)
    monkeypatch.setattr(config, "worktree_endless_go", lambda *a, **k: stale)
    monkeypatch.setattr(statuses, "_BINARY", None)
    monkeypatch.setattr(statuses.shutil, "which", lambda _: "/usr/local/bin/endless-go")
    assert statuses._resolve_binary() == "/usr/local/bin/endless-go"


def test_prefers_the_worktree_binary_when_it_can_answer(monkeypatch, tmp_path):
    """The fallback must not defeat the preference: a worktree build that DOES
    know `task-status` still wins, which is what lets the branch adding a group
    exercise it before landing."""
    good = tmp_path / "endless-go"
    good.write_text("#!/bin/sh\nexit 0\n")
    good.chmod(0o755)
    monkeypatch.setattr(config, "worktree_endless_go", lambda *a, **k: good)
    monkeypatch.setattr(statuses, "_BINARY", None)
    monkeypatch.setattr(statuses.shutil, "which", lambda _: "/usr/local/bin/endless-go")
    assert statuses._resolve_binary() == str(good)


def test_resolution_is_memoized_but_the_vocabulary_is_not(monkeypatch):
    """The probe costs a spawn, so the chosen BINARY is remembered. What it
    answered never is — caching the vocabulary is how this would stop being a
    client and start being a second registry."""
    monkeypatch.setattr(statuses, "_BINARY", "/sentinel/endless-go")
    monkeypatch.setattr(config, "worktree_endless_go", lambda *a, **k: None)
    monkeypatch.setattr(statuses.shutil, "which", lambda _: None)
    assert statuses._binary() == "/sentinel/endless-go"

    import inspect
    for name in ("get", "has", "sql_list", "rank", "label", "glyph", "groups"):
        body = inspect.getsource(getattr(statuses, name))
        assert "_run(" in body, f"{name} does not shell out"
        assert "cache" not in body.lower(), f"{name} looks like it caches"


def test_falls_back_to_path_when_the_worktree_binary_is_absent(monkeypatch, tmp_path):
    """Outside a worktree, and inside one that has not been built yet, PATH is
    the answer — not an error."""
    monkeypatch.setattr(config, "worktree_endless_go", lambda *a, **k: tmp_path / "nope")
    monkeypatch.setattr(statuses, "_BINARY", None)
    monkeypatch.setattr(statuses.shutil, "which", lambda _: "/usr/local/bin/endless-go")
    assert statuses._resolve_binary() == "/usr/local/bin/endless-go"


def test_resolution_ignores_the_db_context(monkeypatch, tmp_path):
    """Unlike event_bridge's resolver, this one has no --db gate: it opens no
    database, so there is no schema baseline to mismatch — and it must answer
    before the --db flag has even been parsed."""
    worktree_bin = tmp_path / "endless-go"
    worktree_bin.write_text("#!/bin/sh\nexit 0\n")
    worktree_bin.chmod(0o755)
    monkeypatch.setattr(config, "worktree_endless_go", lambda *a, **k: worktree_bin)
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", None)
    monkeypatch.setattr(statuses, "_BINARY", None)
    assert statuses._resolve_binary() == str(worktree_bin)
