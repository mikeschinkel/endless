"""Tests for `endless session order` spec parsing + validation (E-1683).

The Python layer's job is parse + validate + payload-build (the groups
array). The end-to-end flow through endless-go event + the Go executor
(membership validation, replace-all do_order rewrite) is tested on the Go
side; here we focus on the Python contract.
"""

import pytest
import click

from endless.session_order_cmd import _parse_compact_spec, _parse_json_spec


# --- Compact spec, happy path ----------------------------------------------

def test_compact_sequence_and_parallel():
    # The canonical plan example: whitespace advances, `|` groups parallel.
    assert _parse_compact_spec("E-100 E-101|E-102 E-103") == [
        ["E-100"], ["E-101", "E-102"], ["E-103"],
    ]


def test_compact_single_task():
    assert _parse_compact_spec("E-7") == [["E-7"]]


def test_compact_normalizes_case_and_whitespace():
    assert _parse_compact_spec("  e-5   E-6|e-7  ") == [["E-5"], ["E-6", "E-7"]]


# --- Compact spec, rejections ----------------------------------------------

@pytest.mark.parametrize("bad,fragment", [
    ("", "empty spec"),
    ("E-1 E-1", "listed more than once"),
    ("E-1|E-1", "listed more than once"),
    ("foo", "malformed task id"),
    ("E-", "malformed task id"),
    ("E-1|", "malformed task id"),
    ("|E-1", "malformed task id"),
    ("E1", "malformed task id"),
])
def test_compact_rejects(bad, fragment):
    with pytest.raises(click.ClickException) as exc:
        _parse_compact_spec(bad)
    assert fragment in exc.value.message


# --- JSON spec, happy path -------------------------------------------------

def test_json_array_of_groups():
    assert _parse_json_spec('[["E-100"], ["E-101", "E-102"], ["E-103"]]') == [
        ["E-100"], ["E-101", "E-102"], ["E-103"],
    ]


def test_json_normalizes_case():
    assert _parse_json_spec('[["e-5"]]') == [["E-5"]]


# --- JSON spec, rejections -------------------------------------------------

@pytest.mark.parametrize("bad,fragment", [
    ("not json", "malformed JSON"),
    ("[]", "non-empty array of groups"),
    ('"E-1"', "non-empty array of groups"),
    ("[[]]", "non-empty array"),
    ('[["E-1", "E-1"]]', "listed more than once"),
    ('[["E-1"], ["E-1"]]', "listed more than once"),
    ('[["foo"]]', "malformed task id"),
    ('[[1]]', "must be a string"),
])
def test_json_rejects(bad, fragment):
    with pytest.raises(click.ClickException) as exc:
        _parse_json_spec(bad)
    assert fragment in exc.value.message
