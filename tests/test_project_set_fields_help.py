"""E-1934: `project set`'s refusal must not read as an inventory of the config.

The message used to end "Settable fields: description, label, language, name,
status", which reads as "the config file has five fields". It actually means
"this command writes five of them" — the file also carries content, matchers,
minimizer and others, edited by hand. A reader who believed the first reading
went looking for a flag that does not exist instead of opening the file.
"""

import click
import pytest

from endless.set_cmd import FILE_ONLY_FIELDS, SETTABLE_FIELDS, _fields_help, set_field


def test_the_two_field_sets_do_not_overlap():
    assert not (set(FILE_ONLY_FIELDS) & SETTABLE_FIELDS)


def test_help_says_what_this_command_writes():
    help_text = _fields_help()
    assert "`project set` writes:" in help_text
    for f in SETTABLE_FIELDS:
        assert f in help_text


def test_help_names_the_file_and_the_keys_that_live_only_there():
    help_text = _fields_help()
    assert ".endless/config.json" in help_text
    for f in FILE_ONLY_FIELDS:
        assert f in help_text


@pytest.mark.parametrize("field", ["content", "minimizer", "matchers"])
def test_a_file_only_key_is_refused_with_a_route_not_a_dead_end(field, monkeypatch):
    # The unknown-field check runs after project resolution, so a fake project
    # stands in; the refusal under test is the one about the FIELD.
    monkeypatch.setattr(
        "endless.set_cmd.resolve_project",
        lambda name, hint=None: {"name": "endless", "path": "~/x", "id": 1},
    )
    with pytest.raises(click.ClickException) as exc:
        set_field(f"endless.{field}=whatever")
    msg = str(exc.value)
    assert f"Unknown field '{field}'" in msg
    assert ".endless/config.json" in msg, "the refusal must say where to edit it"


def test_a_malformed_expression_also_gets_the_inventory():
    with pytest.raises(click.ClickException) as exc:
        set_field("no-equals-sign-here !")
    assert ".endless/config.json" in str(exc.value)
