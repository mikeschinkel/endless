"""E-2126: `task show`'s machine payload, `--brief`, and the children count.

Three properties are pinned here.

1. **The display flags gate the human renderer, not the machine format.** Under
   E-1601 `--json` mirrored the terminal's flag gating, so a field holding 6503
   characters came back as `"analysis": null`. A consumer that did not know to
   pass `--analysis` read a populated task as an empty one.

2. **`null` means exactly one thing: the field is empty.** It used to mean both
   "empty" and "withheld pending a flag you did not pass", and the two were
   indistinguishable for `description`, which had no `_chars` companion at all.
   The biconditional `<field>_chars == 0` iff `<field> is None` is what makes
   `null` unambiguous, so it is asserted as a matrix over every flag
   combination rather than as a single case.

3. **Children are structure, so every render advertises them.** A count in the
   header block and in both machine formats; the list itself still behind
   `--children`; and the `— Children —` section in slot 2, ahead of the
   flag-gated long prose that could otherwise push it off-screen.

`--brief[=N]` is the opt-in light payload. It truncates rather than omits, so a
populated field stays a string — which is the whole reason it can exist without
giving `null` a second meaning back.
"""

import json

import pytest
from click.testing import CliRunner

from endless import db
from endless.cli import BRIEF_CHARS, main

BODY_FIELDS = ("description", "analysis", "plan", "outcome")

# Every way a caller can ask for more or less of the human render. The payload
# assertions run across all of them because the claim being made is that NONE of
# them changes the JSON.
FLAG_COMBOS = [
    (),
    ("--all-fields",),
    ("--no-description",),
    ("--analysis",),
    ("--plan",),
    ("--outcome",),
    ("--children",),
    ("--brief",),
    ("--brief=40",),
    ("--all-fields", "--brief"),
    ("--no-description", "--brief"),
]


def _add_task(title: str, *, type_id: int | None = 1, parent: int | None = None,
              **fields) -> int:
    cols = ", ".join(fields)
    marks = ", ".join("?" for _ in fields)
    cur = db.execute(
        f"INSERT INTO tasks (project_id, title, status, type_id, parent_id, phase, "
        f"created_at, updated_at{', ' + cols if cols else ''}) "
        f"VALUES (1, ?, 'ready', ?, ?, 'now', '2026-01-01T00:00:00', "
        f"'2026-01-01T00:00:00'{', ' + marks if marks else ''})",
        (title, type_id, parent, *fields.values()),
    )
    return cur.lastrowid


def _run(*args: str):
    result = CliRunner().invoke(main, ["task", "show", *args])
    assert result.exit_code == 0, result.output
    return result.output


def _json(*args: str) -> dict:
    return json.loads(_run(*args, "--json"))


def _sections(output: str) -> list[str]:
    """The `— Title —` section headers, in the order they were emitted."""
    return [
        line.strip().strip("—").strip()
        for line in output.splitlines()
        if line.startswith("— ") and line.rstrip().endswith(" —")
    ]


# ─── the reported defect ──────────────────────────────────────────────────────


def test_json_returns_analysis_body_with_no_flags(seeded_project_at_cwd):
    """The bug as filed: 6503 characters of analysis returned as null because
    the human renderer's display flag also gated the machine format."""
    body = "## Observed\n\n" + ("analysis prose. " * 500)
    tid = _add_task("Sample", analysis=body)
    payload = _json(f"E-{tid}")
    assert payload["analysis"] == body
    assert payload["analysis_chars"] == len(body)


@pytest.mark.parametrize("field", BODY_FIELDS)
def test_display_flags_do_not_change_the_json_payload(seeded_project_at_cwd, field):
    """Every body field, every flag combination, one payload. This is the rule
    the fix installs: display flags belong to the display."""
    tid = _add_task("Sample", **{f: f"{f} body" for f in BODY_FIELDS})
    baseline = _json(f"E-{tid}")
    for combo in FLAG_COMBOS:
        if "--brief" in combo or "--brief=40" in combo:
            continue  # --brief legitimately changes the payload; asserted below
        assert _json(f"E-{tid}", *combo)[field] == baseline[field], \
            f"{field} differs under {combo or '(no flags)'}"
    assert baseline[field] == f"{field} body"


def test_no_description_no_longer_loses_the_description(seeded_project_at_cwd):
    """`--no-description` had no `description_chars` to contradict it, so under
    it withheld and empty were indistinguishable. Now it does not apply."""
    tid = _add_task("Sample", description="the description body")
    payload = _json(f"E-{tid}", "--no-description")
    assert payload["description"] == "the description body"
    assert payload["description_chars"] == len("the description body")
    # ...while the human renderer still honours it.
    assert "the description body" not in _run(f"E-{tid}", "--no-description")


# ─── null means empty, and only empty ─────────────────────────────────────────


@pytest.mark.parametrize("combo", FLAG_COMBOS, ids=lambda c: "+".join(c) or "bare")
def test_chars_zero_iff_body_null(seeded_project_at_cwd, combo):
    """The invariant that gives `null` exactly one meaning, asserted across the
    whole flag matrix on a task with two populated and two empty fields — plus
    the third spelling of absent, `''`, which normalizes to null like the rest."""
    tid = _add_task(
        "Sample",
        description="a description body",
        analysis="an analysis body",
        plan="",          # empty string in the column, not NULL
        outcome=None,     # NULL in the column
    )
    payload = _json(f"E-{tid}", *combo)
    for field in BODY_FIELDS:
        count = payload[f"{field}_chars"]
        assert isinstance(count, int), f"{field}_chars must never be null"
        assert (count == 0) == (payload[field] is None), (
            f"{field}_chars == 0 must hold if and only if {field} is null "
            f"(got {count!r} / {payload[field]!r} under {combo or '(no flags)'})"
        )
    assert payload["plan"] is None, "an empty string must spell absent as null"
    assert payload["outcome"] is None


@pytest.mark.parametrize("combo", FLAG_COMBOS, ids=lambda c: "+".join(c) or "bare")
def test_no_chars_key_is_ever_null(seeded_project_at_cwd, combo):
    """On a task with nothing in it at all — the case where a nullable count
    would be most tempting."""
    tid = _add_task("Sample")
    payload = _json(f"E-{tid}", *combo)
    for field in BODY_FIELDS:
        assert payload[f"{field}_chars"] == 0


# ─── --brief ──────────────────────────────────────────────────────────────────


def test_brief_truncates_rather_than_omitting(seeded_project_at_cwd):
    body = "x" * 1000
    tid = _add_task("Sample", analysis=body)
    payload = _json(f"E-{tid}", "--brief")
    assert payload["analysis"] == "x" * BRIEF_CHARS + "…"
    assert payload["analysis_chars"] == 1000, \
        "the count reports the STORED length, not the preview's"


def test_brief_n_honours_n(seeded_project_at_cwd):
    tid = _add_task("Sample", analysis="y" * 1000)
    assert _json(f"E-{tid}", "--brief=40")["analysis"] == "y" * 40 + "…"


def test_brief_leaves_a_short_field_whole(seeded_project_at_cwd):
    """No ellipsis on a field at or under the limit, so the absence of `…`
    reliably means 'not truncated'."""
    tid = _add_task("Sample", analysis="z" * 40, plan="z" * 41)
    payload = _json(f"E-{tid}", "--brief=40")
    assert payload["analysis"] == "z" * 40
    assert not payload["analysis"].endswith("…")
    assert payload["plan"] == "z" * 40 + "…"


def test_brief_never_yields_null_for_a_populated_field(seeded_project_at_cwd):
    """The point of truncating instead of omitting: the type stays stable."""
    tid = _add_task("Sample", **{f: "q" * 900 for f in BODY_FIELDS})
    payload = _json(f"E-{tid}", "--brief=10")
    for field in BODY_FIELDS:
        assert payload[field] == "q" * 10 + "…"


def test_brief_wins_over_the_display_flags_in_the_human_render(seeded_project_at_cwd):
    """One flag, one meaning — previews, not bodies — so `--all-fields --brief`
    yields previews rather than the full bodies --all-fields would have."""
    tid = _add_task("Sample", analysis="a" * 400, plan="t" * 400)
    out = _run(f"E-{tid}", "--all-fields", "--brief=30")
    assert "a" * 30 + "…" in out
    assert "a" * 400 not in out
    assert "t" * 30 + "…" in out


def test_brief_reveals_a_gated_field_as_a_preview(seeded_project_at_cwd):
    """--brief alone shows what the placeholder would have hidden, truncated —
    the human counterpart of `--llm --brief` upgrading a bare count."""
    tid = _add_task("Sample", analysis="a" * 400)
    out = _run(f"E-{tid}", "--brief=30")
    assert "— Analysis —" in out
    assert "a" * 30 + "…" in out
    assert "(--analysis to display)" not in out


def test_brief_rejects_a_length_below_one(seeded_project_at_cwd):
    tid = _add_task("Sample")
    result = CliRunner().invoke(main, ["task", "show", f"E-{tid}", "--brief=0"])
    assert result.exit_code != 0
    assert "character count" in result.output


def test_brief_names_the_fix_when_it_swallows_a_task_id(seeded_project_at_cwd):
    """`--brief` carries an optional value, so it eats the next token. A bare
    integer-range error would show the reader their own task id being called a
    bad number."""
    result = CliRunner().invoke(main, ["task", "show", "--brief", "E-1"])
    assert result.exit_code != 0
    assert "looks like a task id" in result.output
    assert "'E-1 --brief'" in result.output


# ─── the human render is unchanged by default ─────────────────────────────────


def test_default_human_render_still_hides_the_bodies(seeded_project_at_cwd):
    """E-1601's placeholder is what a person reads; --brief adds a mode, it
    replaces nothing."""
    tid = _add_task("Sample", analysis="a" * 400)
    out = _run(f"E-{tid}")
    assert "400 chars (--analysis to display)" in out
    assert "— Analysis —" not in out


def test_llm_default_output_is_unchanged_but_for_the_children_lines(
        seeded_project_at_cwd):
    """The regression most likely to go unseen, pinned line by line. A childless
    task emits no children lines at all, so this is E-1601's `--llm` output
    verbatim."""
    tid = _add_task("Sample", description="Sample", analysis="a" * 12,
                    plan="t" * 34, outcome="o" * 56)
    assert _run(f"E-{tid}", "--llm").splitlines() == [
        f"# E-{tid} Sample",
        "project=test",
        "type=todo phase=now status=ready",
        "created=2026-01-01T00:00:00",
        "updated=2026-01-01T00:00:00",
        "analysis_chars=12",
        "plan_chars=34",
        "outcome_chars=56",
    ]


def test_llm_brief_upgrades_a_bare_count_to_a_preview(seeded_project_at_cwd):
    tid = _add_task("Sample", analysis="a" * 400)
    out = _run(f"E-{tid}", "--llm", "--brief=30")
    assert "## Analysis" in out
    assert "a" * 30 + "…" in out
    assert "analysis_chars=" not in out


# ─── children: counted everywhere, listed on request ──────────────────────────


def test_children_count_is_present_and_zero_on_a_childless_task(
        seeded_project_at_cwd):
    """0 and {} rather than absent keys — an absent key leaves 'childless'
    unsaid, which is the failure this whole task is about."""
    payload = _json(f"E-{_add_task('Lonely')}")
    assert payload["children_count"] == 0
    assert payload["children_by_type"] == {}
    assert "children" not in payload


def test_children_by_type_is_ordered_by_descending_count_then_alphabetically(
        seeded_project_at_cwd):
    parent = _add_task("Parent", type_id=4)
    for _ in range(3):
        _add_task("todo child", type_id=1, parent=parent)
    for _ in range(3):
        _add_task("bugfix child", type_id=2, parent=parent)
    _add_task("research child", type_id=3, parent=parent)
    payload = _json(f"E-{parent}")
    assert payload["children_count"] == 7
    # bugfix before todo on the 3-3 tie, alphabetically; research last on count.
    assert list(payload["children_by_type"].items()) == [
        ("bugfix", 3), ("todo", 3), ("research", 1),
    ]
    assert sum(payload["children_by_type"].values()) == payload["children_count"]


def test_children_list_stays_gated_behind_the_flag(seeded_project_at_cwd):
    parent = _add_task("Parent", type_id=4)
    _add_task("A child", parent=parent)
    assert "children" not in _json(f"E-{parent}")
    assert len(_json(f"E-{parent}", "--children")["children"]) == 1


def test_llm_emits_children_lines_only_when_there_are_children(
        seeded_project_at_cwd):
    parent = _add_task("Parent", type_id=4)
    _add_task("t1", type_id=1, parent=parent)
    _add_task("t2", type_id=1, parent=parent)
    _add_task("b1", type_id=2, parent=parent)
    out = _run(f"E-{parent}", "--llm")
    assert "children_count=3" in out
    assert "children_by_type=todo:2,bugfix:1" in out
    assert "children_count=" not in _run(f"E-{_add_task('Lonely')}", "--llm")


def test_children_header_line_single_type(seeded_project_at_cwd):
    parent = _add_task("Parent", type_id=4)
    for _ in range(3):
        _add_task("child", type_id=1, parent=parent)
    assert "Children:" in _run(f"E-{parent}")
    assert "3 todo" in _run(f"E-{parent}")


def test_children_header_line_mixed_types(seeded_project_at_cwd):
    parent = _add_task("Parent", type_id=4)
    for _ in range(3):
        _add_task("child", type_id=1, parent=parent)
    for _ in range(2):
        _add_task("child", type_id=2, parent=parent)
    assert "5 tasks (3 todo, 2 bugfix)" in _run(f"E-{parent}")


def test_children_header_line_omitted_when_childless(seeded_project_at_cwd):
    assert "Children:" not in _run(f"E-{_add_task('Lonely')}")


def test_children_header_line_survives_the_children_flag(seeded_project_at_cwd):
    """Children are ALWAYS advertised as a count; --children adds the list, it
    does not replace the advertisement."""
    parent = _add_task("Parent", type_id=4)
    _add_task("child", type_id=1, parent=parent)
    out = _run(f"E-{parent}", "--children")
    assert "1 todo" in out
    assert "— Children —" in out


def test_untyped_children_are_labelled_rather_than_blank(seeded_project_at_cwd):
    """Older rows carry a null type_id. The join's empty string renders as
    `2 ` in the header line and as a `""` key in JSON — both read as a bug."""
    parent = _add_task("Parent", type_id=4)
    _add_task("child", type_id=None, parent=parent)
    _add_task("child", type_id=None, parent=parent)
    assert "2 untyped" in _run(f"E-{parent}")
    assert _json(f"E-{parent}")["children_by_type"] == {"untyped": 2}


# ─── children render in slot 2 ────────────────────────────────────────────────


def test_children_section_renders_in_slot_2(seeded_project_at_cwd):
    parent = _add_task("Parent", type_id=4, description="a description body",
                       analysis="an analysis body", plan="a plan body",
                       outcome="an outcome body")
    _add_task("child", parent=parent)
    assert _sections(_run(f"E-{parent}", "--all-fields")) == [
        "Description", "Children", "Analysis", "Plan", "Outcome",
    ]


@pytest.mark.parametrize("fields,args,expected", [
    # Description suppressed by the flag.
    (dict(description="d", analysis="a"), ("--no-description", "--analysis",
                                           "--children"),
     ["Children", "Analysis"]),
    # Description skipped because it merely repeats the title.
    (dict(description="Parent", analysis="a"), ("--analysis", "--children"),
     ["Children", "Analysis"]),
    # Analysis's flag set but the field is empty.
    (dict(description="d", analysis=""), ("--analysis", "--children"),
     ["Description", "Children"]),
    # Analysis's flag simply unset.
    (dict(description="d", analysis="a"), ("--children",),
     ["Description", "Children"]),
    # Children is the ONLY section that renders.
    (dict(description="Parent"), ("--children",), ["Children"]),
    # Trailing sections present, everything ahead of Children absent.
    (dict(description="Parent", plan="t", outcome="o"),
     ("--plan", "--outcome", "--children"), ["Children", "Plan", "Outcome"]),
])
def test_children_hold_slot_2_across_the_presence_matrix(
        seeded_project_at_cwd, fields, args, expected):
    """Stated as a slot rather than as 'before Analysis' because every section
    in the sequence is conditional; naming a neighbour would make the rule
    depend on whether that neighbour exists."""
    parent = _add_task("Parent", type_id=4, **fields)
    _add_task("child", parent=parent)
    assert _sections(_run(f"E-{parent}", *args)) == expected


def test_children_none_still_renders_under_the_flag(seeded_project_at_cwd):
    """A direct question gets a direct answer, even though the header line is
    omitted for a childless task."""
    out = _run(f"E-{_add_task('Lonely')}", "--children")
    assert "— Children —" in out
    assert "(none)" in out
