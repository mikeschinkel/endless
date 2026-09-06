"""E-1744: the content gate at cli._resolve_content_flag.

Three checks, and they are two different KINDS of check (E-1934):

  MIS-PASSED FLAG — the whole value IS a path token, absolute or relative.
    Inline flags store their argument verbatim, so passing a path silently
    discarded the intended content: the corruption that lost E-1626/E-1564.
    Inline-only, and correctly so — handing a path to --<name>-file is that
    flag's entire purpose, so there is nothing to catch on the file branch.

  ABSOLUTE PATH anywhere in the content.
  LINE CITATION anywhere in the content (name.ext:NNN).
    Rules about what durable content may SAY. Both run on RESOLVED content, so
    a file is judged exactly as inline text is. Content does not become portable,
    or stop going stale, because it arrived in a file.

Relative tokens mid-content are always allowed. --allow-path <regex> (repeatable)
exempts a matching absolute path; the citation check has no escape hatch by
decision. These tests hit the predicates directly; the per-task verify suites
cover the same rules end-to-end via CLI.

E-1794 narrowed what counts as a path — see the section at the foot of this
file — and reordered the absolute-path advice to lead with --allow-path.
"""

import click
import pytest

from endless.cli import (
    _guard_content_rules,
    _guard_inline_content,
    _resolve_content_flag,
)


def _gate(value, name="text", allow=()):
    """Both checks a caller of an inline flag actually meets, in order —
    the same pair, in the same sequence, that _resolve_content_flag runs."""
    _guard_inline_content(value, name, allow)
    _guard_content_rules(value, name, allow, whole_value_checked=True)


def _blocked(value, name="text", allow=()):
    with pytest.raises(click.ClickException) as exc:
        _gate(value, name, allow)
    return str(exc.value)


# ─── Rule 1 — the whole value is a single path token ────────────────────────

@pytest.mark.parametrize("value", [
    "/abs/x.md",
    "./x.md",
    "../x.md",
    "~/x.txt",
    "/tmp/e-1.md",          # gone or present — blocked either way
    "foo.md",               # bare filename, no slash
    "docs/guide/index.md",  # relative, has slash
    "  /tmp/x.md  ",        # surrounding whitespace stripped first
])
def test_rule1_whole_value_path_blocks(value):
    msg = _blocked(value)
    assert "received a file path" in msg
    assert "--text-file" in msg


def test_rule1_message_names_the_paired_file_flag():
    assert "--outcome-file" in _blocked("/tmp/x.md", name="outcome")
    assert "--analysis-file" in _blocked("/tmp/x.md", name="analysis")


# ─── Rule 2 — an absolute path appears anywhere in the content ──────────────

@pytest.mark.parametrize("value", [
    "See /tmp/x.md for detail",
    "the plan lives at /Users/x/plan.md inside an otherwise long paragraph here",
    "first line ok\nsecond line points at /tmp/buried.md\nthird ok",
    "wrapped (/tmp/x.md) in parens",       # surrounding punctuation trimmed
    "trailing /tmp/x.md.",                  # trailing sentence period trimmed
])
def test_rule2_absolute_in_content_blocks(value):
    assert "contains an absolute path" in _blocked(value)


# ─── Allowed — relative paths & plain content pass ──────────────────────────

@pytest.mark.parametrize("value", [
    "edit internal/hookcmd/claude.go then run just build",
    "see notes in ./foo.md for context",
    "edits src/x.py\nand ./y.md too",       # multiline, only relative
    "just a plain inline note with no path",
    "compare a/b and and/or which contain slashes but are not absolute",
    "@alice reviewed this",                  # @mention is not path-shaped
    "https://github.com/org/repo/blob/main/x.md",   # whole-value Git file URL
    "see https://github.com/org/repo/pull/1 for the discussion",  # URL mid-content
])
def test_allowed_content_passes(value):
    # Must not raise.
    _gate(value, "text", ())


# ─── Escape hatch — --allow-path <regex> (repeatable) ───────────────────────

def test_allow_path_exempts_matching_absolute():
    _gate("see /opt/corp/spec.md for the API", "text",
                          (r"^/opt/corp/",))


def test_allow_path_second_nonmatching_still_blocks():
    assert "contains an absolute path" in _blocked(
        "see /opt/corp/spec.md and /tmp/other.md",
        allow=(r"^/opt/corp/",),
    )


def test_allow_path_repeatable_covers_multiple():
    _gate("refs /opt/corp/spec.md and /opt/acme/api.md", "text",
                          (r"^/opt/corp/", r"^/opt/acme/"))


def test_allow_path_exempts_rule1_whole_value_when_absolute():
    # A whole-value absolute path matching an allow regex is exempt from Rule 1.
    _gate("/opt/corp/spec.md", "text", (r"^/opt/corp/",))


# ─── Built-in allowed paths — endless's own config + cache dirs ──────────────

@pytest.mark.parametrize("value", [
    "~/.config/endless/endless.db",                          # Rule 1 whole value
    "the main database is at ~/.config/endless/endless.db here",  # Rule 2 in prose
    "sandbox lives at ~/.cache/endless/sandboxes/e-1/endless",
])
def test_builtin_config_cache_dirs_allowed_without_allow_path(value):
    # endless's own config/cache dirs are always exempt — no --allow-path needed.
    _gate(value, "text", ())


def test_builtin_absolute_home_expanded_form_allowed(tmp_path, monkeypatch):
    # The /Users/... (already-absolute) spelling of ~/.config/endless is exempt.
    from pathlib import Path
    cfg = Path.home() / ".config" / "endless" / "endless.db"
    _gate(f"see {cfg} for the ledger", "text", ())


def test_builtin_honors_xdg_config_home(monkeypatch):
    # A path under $XDG_CONFIG_HOME/endless is exempt (resolution honors XDG).
    monkeypatch.setenv("XDG_CONFIG_HOME", "/tmp/fake-xdg-cfg")
    _gate("cfg at /tmp/fake-xdg-cfg/endless/config.json here",
                          "text", ())
    # HOME-anchored ~/.config/endless stays exempt even under XDG redirection.
    _gate("db at ~/.config/endless/endless.db here", "text", ())
    # A sibling under the XDG root but NOT under /endless still blocks.
    assert "contains an absolute path" in _blocked(
        "x at /tmp/fake-xdg-cfg/other/y here")


def test_builtin_honors_xdg_cache_home(monkeypatch):
    # A path under $XDG_CACHE_HOME/endless is exempt.
    monkeypatch.setenv("XDG_CACHE_HOME", "/tmp/fake-xdg-cache")
    _gate("sandbox at /tmp/fake-xdg-cache/endless/sandboxes/e-1 here",
                          "text", ())


def test_builtin_non_endless_absolute_still_blocks():
    # The built-in exemption is scoped to endless's dirs only.
    assert "contains an absolute path" in _blocked(
        "the plan is at /Users/x/plan.md here")
    # A sibling of the config dir is not endless-owned.
    assert "contains an absolute path" in _blocked(
        "cfg at ~/.config/other/thing.db here")


def test_allow_path_does_not_rescue_relative_whole_value():
    # --allow-path only exempts absolute paths; a relative whole value still
    # blocks (it is always the mis-passed-file case).
    assert "received a file path" in _blocked("./x.md", allow=(r"^/opt/",))


# ─── _resolve_content_flag integration ──────────────────────────────────────

def test_resolve_none_and_plain_pass_through():
    assert _resolve_content_flag(None, None, "text") is None
    assert _resolve_content_flag("plain note", None, "text") == "plain note"


def test_resolve_inline_path_blocks():
    with pytest.raises(click.ClickException):
        _resolve_content_flag("/tmp/x.md", None, "text")


def test_resolve_file_content_is_gated_too(tmp_path):
    """E-1934 reversed this. It used to assert the opposite — that a file's
    CONTENT was trusted because --<name>-file is the sanctioned way to load a
    file. That conflated the flag with what it carries. E-2089 measured the cost:
    a third of stored plans hold a file-and-line reference, and a plan is
    long-form, so it arrives by --text-file — through the branch that was exempt.
    """
    p = tmp_path / "plan.md"
    p.write_text("the sandbox lives at /tmp/anything and that is fine here")
    with pytest.raises(click.ClickException) as exc:
        _resolve_content_flag(None, str(p), "text")
    assert "absolute path" in str(exc.value)


def test_resolve_file_content_without_a_violation_still_loads(tmp_path):
    p = tmp_path / "plan.md"
    p.write_text("the sandbox lives under the endless cache dir, resolved at run time")
    assert _resolve_content_flag(None, str(p), "text").startswith("the sandbox")


# ═══ E-1794 — slash-separated notation is not a path ═══════════════════════
#
# Both rules keyed off "has a slash in it", so a runner type/subtype (pytest/uv)
# was refused as a mis-passed file and a bare '/' being named as a delimiter was
# refused as an absolute path. Filing E-1789's ED-1535/1536/1537 needed three
# --allow-path workarounds for content holding no path at all.
#
# A token is a path only on one of three lexical signals: it is absolute, it
# carries an explicit ./ ../ ~/ prefix, or it ends in a filename extension.

from endless.cli import _is_absolute_path, _is_path_shaped


@pytest.mark.parametrize("value", [
    "pytest/uv",                    # the reported case — a runner type/subtype
    "docs/plan",                    # slash, no extension, no ./ prefix
    "and/or",
])
def test_slash_separated_notation_is_not_a_whole_value_path(value):
    # Must not raise: Rule 1 no longer fires on a bare slash-separated token.
    _gate(value, "description", ())


def test_extension_signal_still_wins_over_notation_as_a_whole_value():
    """A known limit, recorded so it is not mistaken for a regression.

    ED-1537's in-family vendor form ends in what a lexical test cannot tell from
    a filename extension, so as an ENTIRE value it still reads as a path. No
    rule separates '.uv' from '.md' without a known-extension list, and dropping
    the extension signal would let `--text plan.md` through — the E-1626/E-1564
    corruption. The case is degenerate anyway: a description is a 2-3 sentence
    blurb, never one bare token. Where the notation actually appears — inside
    prose — it passes, which the next test pins.
    """
    assert "received a file path" in _blocked("pytest/vnd.newclarity.uv")


def test_notation_in_prose_passes_including_the_vendor_form():
    _gate(
        "an in-family custom type is pytest/vnd.newclarity.uv, never "
        "vnd/newclarity/foo", "description", ())


@pytest.mark.parametrize("value", [
    "/",                                          # whole value
    "'/' splits family from variant (pytest/uv)",  # ED-1537, verbatim
    "written as type / subtype",                   # spaced alternative
    "a // comment marker",                         # a run of slashes is still nothing
])
def test_bare_slash_is_punctuation_not_an_absolute_path(value):
    # Must not raise: a leading slash counts only when a path follows it.
    _gate(value, "description", ())


@pytest.mark.parametrize("value", [
    "/abs/x.md", "/tmp/e-1.md", "./x.md", "../x.md", "~/x.txt",
    "foo.md", "docs/guide/index.md",
])
def test_the_three_path_signals_still_block_as_a_whole_value(value):
    # The narrowing must not reopen E-1626/E-1564: a mis-passed file still blocks.
    assert "received a file path" in _blocked(value)


@pytest.mark.parametrize("value", [
    "See /tmp/x.md for detail",
    "sandbox at /tmp/sbx here",     # absolute, no extension — still a path
])
def test_real_absolute_paths_in_prose_still_block(value):
    assert "contains an absolute path" in _blocked(value)


def test_is_absolute_path_predicate():
    assert _is_absolute_path("/tmp/x")
    assert not _is_absolute_path("/")
    assert not _is_absolute_path("//")
    assert not _is_absolute_path("pytest/uv")
    # /tmp is the ambiguous one-segment case — see the E-1794-reopened section.
    assert not _is_absolute_path("/tmp")


def test_is_path_shaped_leaves_urls_alone():
    assert not _is_path_shaped("https://github.com/org/repo/blob/main/x.md")


# ─── Rule 2's advice: --allow-path leads (E-1794 part 2) ────────────────────
#
# The flag was the last clause of the last sentence, behind "put real content
# inline" — so an agent reading the whole message still rephrased the content to
# satisfy the gate, which is the opposite of what the gate is for.

def test_allow_path_leads_the_rule2_advice():
    msg = _blocked("see /tmp/x.md here")
    assert msg.index("--allow-path") < msg.index("project-relative")


def test_rule2_does_not_offer_a_file_flag_for_a_description():
    # description is one line capped at 1024 chars; --description-file cannot be
    # the remedy, and following it earned a second refusal on E-2094.
    msg = _blocked("see /tmp/x.md here", name="description")
    assert "--description-file" not in msg
    assert "short metadata" in msg
    assert "--allow-path" in msg


@pytest.mark.parametrize("name", ["text", "analysis", "outcome"])
def test_the_file_flag_is_no_longer_offered_as_the_way_out(name):
    """E-1934: the file branch is gated now, so naming --<name>-file as the
    remedy would hand the reader a route that refuses them one command later."""
    msg = _blocked("see /tmp/x.md here", name=name)
    assert f"--{name}-file" not in msg
    assert "--allow-path" in msg
    assert "project-relative" in msg


def test_rule2_keeps_explaining_why():
    assert "non-portable" in _blocked("see /tmp/x.md here")


# ═══ E-1794 reopened — a slash command is not an absolute path ═════════════
#
# The first pass defined absolute as "a leading slash followed by a path", which
# still read every one-segment leading-slash token as a path. So a lesson could
# not name a slash command (/whats-left, /loop), and — the sharpest case — the
# gate's OWN refusal text ("a /tmp path is lost when a worktree drops") could
# not be written into a lesson or a decision by the tool that emits it.
#
# Reported against `lesson write` and suspected of `decision add/update`. All of
# them, plus task add/update and the status-transition verbs, funnel through
# _resolve_content_flag — one composition point, so one defect, not three.

@pytest.mark.parametrize("token,is_path", [
    ("/",                 False),   # nothing after the slash
    ("//",                False),
    ("/tmp/x.md",         True),    # two segments
    ("/Users/x/plan.md",  True),    # the gate's primary target
    ("/tmp/sbx",          True),    # two segments, no extension
    ("/plan.md",          True),    # one segment, but carries an extension
    ("/tmp",              False),   # a directory NAMED in prose, not pointed at
    ("/whats-left",       False),   # a slash-command name
    ("/loop",             False),
])
def test_the_rule_is_lexical_and_machine_independent(token, is_path):
    assert _is_absolute_path(token) is is_path


def test_a_one_segment_token_is_decided_not_deferred():
    """The cheap answer is the correct one, so there is no model tier here.

    A model asked whether /tmp is a path says yes — and that would make this
    gate's own sentence, "a /tmp path is lost when a worktree drops",
    unwritable in a lesson again. The mis-passed file this gate exists to catch
    always carries a directory or an extension, so nothing is given up.
    """
    assert not _is_absolute_path("/tmp")
    assert _is_absolute_path("/tmp/plan.md")   # a directory: still caught
    assert _is_absolute_path("/plan.md")       # an extension: still caught


@pytest.mark.parametrize("value", [
    "/whats-left",                                   # whole value
    "the /whats-left skill reports remaining work",  # the reported case
    "run /loop 5m /foo to repeat it",
    "/code-review ultra launches a cloud review",
    "a /tmp path is lost when a worktree drops",     # the gate's own sentence
    "the sandbox lives at /tmp",
])
def test_slash_commands_and_bare_dirs_are_not_absolute_paths(value):
    # Must not raise.
    _gate(value, "text", ())


@pytest.mark.parametrize("value", [
    "the plan is at /Users/mike/plan.md today",
    "the plan lives at /tmp/x.md, see there",
    "the sandbox is at /tmp/sbx for this run",
])
def test_real_absolute_paths_still_block_after_the_reopening(value):
    assert "contains an absolute path" in _blocked(value)


def test_a_root_level_file_is_still_a_path():
    # One segment, but an extension — a filename, not a command.
    assert "received a file path" in _blocked("/plan.md")


# ─── one gate, every verb ───────────────────────────────────────────────────
#
# Reported on `lesson write`; the fix is in the shared predicate, so what has to
# be true is that these verbs reach it rather than carrying gates of their own.
# Asserted on the wiring, not by executing them: `lesson write` appends to the
# project's real lessons log and commits it, which is not something a test suite
# should be doing.

@pytest.mark.parametrize("command_path", [
    ("lesson", "write"),
    ("decision", "add"),
    ("decision", "update"),
    ("task", "add"),
    ("task", "update"),
])
def test_every_content_bearing_verb_carries_the_gates_escape_hatch(command_path):
    from endless.cli import main as root
    cmd = root
    for name in command_path:
        cmd = cmd.get_command(None, name)
        assert cmd is not None, f"no such command: {' '.join(command_path)}"
    flags = {opt for param in cmd.params for opt in getattr(param, "opts", ())}
    assert "--allow-path" in flags


# ═══ E-2008 — the empty-file gate ═══════════════════════════════════════════
#
# `--<name>-file` wrote whatever the file held, so a path that came back empty
# (a failed extraction, a sed that matched nothing) silently replaced existing
# content and the command reported success. Zero bytes is never a legitimate
# value for these fields, so an empty or whitespace-only file is refused
# unconditionally — no --force. Clearing is a separate, field-named act.

from endless.cli import _apply_clear_flags


def _empty_file_error(tmp_path, body, name="analysis", clearable=False):
    p = tmp_path / "extracted.md"
    p.write_text(body)
    with pytest.raises(click.ClickException) as exc:
        _resolve_content_flag(None, str(p), name, clearable=clearable)
    return str(exc.value), p


@pytest.mark.parametrize("body", [
    "",              # zero bytes — the E-1817 case
    " ",
    "\n",
    "\n\n\t  \n",    # whitespace-only is just as much a failed pipeline
])
def test_empty_file_is_refused(tmp_path, body):
    msg, _ = _empty_file_error(tmp_path, body)
    assert "loaded no content" in msg


def test_refusal_names_the_offending_path(tmp_path):
    # Naming the path is the actionable part — the caller has to know WHICH
    # file came back empty to find the step that produced it.
    msg, p = _empty_file_error(tmp_path, "")
    assert str(p) in msg


def test_refusal_distinguishes_zero_bytes_from_whitespace(tmp_path):
    # Which one it is tells you which step of the pipeline failed.
    assert "0 bytes" in _empty_file_error(tmp_path, "")[0]
    assert "all whitespace" in _empty_file_error(tmp_path, "  \n\n")[0]


def test_refusal_names_the_field_and_its_file_flag(tmp_path):
    msg, _ = _empty_file_error(tmp_path, "", name="text")
    assert "--text-file" in msg
    assert "blank text" in msg


def test_refusal_offers_clear_only_where_clear_exists(tmp_path):
    # The `update` verbs carry --clear; `add` and the status-transition verbs
    # do not (there is nothing to clear when a field is first written).
    assert "--clear analysis" in _empty_file_error(
        tmp_path, "", clearable=True)[0]
    assert "--clear" not in _empty_file_error(tmp_path, "")[0]


def test_no_force_style_escape_hatch_exists(tmp_path):
    # Deliberate (E-2008): --force is the flag a mistaken caller reflexively
    # appends after reading a refusal, which would restore the exact failure
    # mode with an audit trail claiming it was intended.
    msg, _ = _empty_file_error(tmp_path, "", clearable=True)
    assert "--force" not in msg


def test_file_with_real_content_still_loads(tmp_path):
    p = tmp_path / "plan.md"
    p.write_text("\n  real content surrounded by blank lines  \n\n")
    # Returned verbatim — the emptiness test strips, the value does not.
    assert _resolve_content_flag(None, str(p), "text") == \
        "\n  real content surrounded by blank lines  \n\n"


def test_inline_empty_string_still_clears(tmp_path):
    # The inline form is unaffected: it names the field and cannot be reached
    # by a failed pipeline, so it stays a legitimate way to empty a field.
    assert _resolve_content_flag("", None, "analysis") == ""


def test_missing_file_still_reports_not_found(tmp_path):
    # The empty gate must not swallow the pre-existing not-found error.
    with pytest.raises(click.ClickException) as exc:
        _resolve_content_flag(None, str(tmp_path / "gone.md"), "text")
    assert "File not found" in str(exc.value)


# ─── --clear <field> — the named escape hatch ───────────────────────────────

def test_clear_sets_the_field_to_empty_string():
    out = _apply_clear_flags(("analysis",), {"analysis": None, "text": None})
    assert out == {"analysis": "", "text": None}


def test_clear_is_repeatable_across_fields():
    out = _apply_clear_flags(("analysis", "text"),
                             {"analysis": None, "text": None})
    assert out == {"analysis": "", "text": ""}


def test_same_field_cleared_twice_is_harmless():
    # The conflict test reads the ORIGINAL map, not the accumulating one, so a
    # repeated --clear text is a no-op rather than a self-conflict.
    out = _apply_clear_flags(("text", "text"), {"text": None})
    assert out == {"text": ""}


@pytest.mark.parametrize("competing", ["real content", ""])
def test_clear_conflicts_with_the_same_fields_value_flag(competing):
    # Both spellings conflict: two flags writing one column is exactly the
    # ambiguity the gate exists to remove.
    with pytest.raises(click.ClickException) as exc:
        _apply_clear_flags(("text",), {"text": competing})
    msg = str(exc.value)
    assert "--clear text conflicts with --text/--text-file" in msg


def test_clear_does_not_disturb_other_fields():
    out = _apply_clear_flags(("analysis",),
                             {"analysis": None, "text": "a plan", "outcome": None})
    assert out == {"analysis": "", "text": "a plan", "outcome": None}


def test_clear_of_nothing_is_a_passthrough():
    resolved = {"text": "a plan", "analysis": None}
    assert _apply_clear_flags((), resolved) == resolved


# ═══ E-1934 — line citations ═══════════════════════════════════════════════
#
# ED-1073 forbids time-frozen specifics in durable content and was widened to
# cover every field. Nothing enforced it: E-2089 measured 129 of 412 stored
# plans carrying a file-and-line reference, and not decaying.
#
# A citation is name.ext:NNN with a KNOWN extension, optionally :COL or -END.
# A bare :NNN is deliberately not matched — nobody writes one, and the shape
# collides with clock times, host:port and ratios.

@pytest.mark.parametrize("value", [
    "the gate is in cli.py:2249 today",
    "internal/monitor/session.go:55 does the upsert",
    "cli.py:12-40 covers the range",
    "cli.py:12:5 is line and column",
    "wrapped `task_cmd.py:4387` in backticks",
    "[the gate](src/endless/cli.py:2249) as a markdown link",
])
def test_line_citations_block(value):
    msg = _blocked(value)
    assert "cites a line number" in msg


@pytest.mark.parametrize("value", [
    "ES-1060 ran 06:00:57 to 06:01:27, thirty seconds",
    "bind to localhost:8080 in development",
    "an aspect ratio of 16:9 or 4:3",
    "a bare :2279 is not a citation anyone writes",
    "version v1.2:30 of the spec",
    "example.com:8080 is a host and port",
])
def test_colon_numbers_that_are_not_citations_pass(value):
    _gate(value)


@pytest.mark.parametrize("value", [
    "see https://example.com/a/b.py:80 for the source",
    "mail user@host.py:12 about it",
    "www.example.md:99 is a website",
])
def test_url_shaped_tokens_are_never_citations(value):
    """Load-bearing, not decoration: rs, py, pl, sh, ml, cc, md and tf are all
    ccTLDs as well as source extensions, so the extension set cannot separate a
    citation from an address on its own."""
    _gate(value)


def test_an_unknown_extension_is_not_a_citation():
    _gate("the record at customer.acme:2249 is unrelated")


def test_the_citation_refusal_names_the_remedy_and_offers_no_escape():
    msg = _blocked("the gate is in cli.py:2249 today")
    assert "no\n  --allow flag" in msg or "no --allow flag" in msg
    assert "go stale" in msg
    assert "function, command or symbol" in msg


def test_citations_block_on_the_file_branch_too(tmp_path):
    """The regression that proves the placement. A plan is long-form, so it
    arrives by --text-file; gating inline only would miss most of them."""
    p = tmp_path / "plan.md"
    p.write_text("Rewrite the helper in src/endless/cli.py:2249 and retest.\n")
    with pytest.raises(click.ClickException) as exc:
        _resolve_content_flag(None, str(p), "text")
    assert "cites a line number" in str(exc.value)
