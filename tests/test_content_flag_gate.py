"""E-1744: the inline-content path gate at cli._resolve_content_flag.

Inline flags (--text/--outcome/--description/--analysis) store their argument
verbatim; passing a file path silently discarded the intended content (the
corruption that lost E-1626/E-1564). Two rules block that:

  Rule 1 — the whole value IS a path token (absolute OR relative).
  Rule 2 — the content contains an ABSOLUTE path anywhere.

Relative tokens mid-content are always allowed; --allow-path <regex> (repeatable)
exempts a matching absolute path from both rules. These tests hit the predicate
directly; tests/tasks/e-1744-verify.sh covers the same rules end-to-end via CLI.
"""

import click
import pytest

from endless.cli import _guard_inline_content, _resolve_content_flag


def _blocked(value, name="text", allow=()):
    with pytest.raises(click.ClickException) as exc:
        _guard_inline_content(value, name, allow)
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
    _guard_inline_content(value, "text", ())


# ─── Escape hatch — --allow-path <regex> (repeatable) ───────────────────────

def test_allow_path_exempts_matching_absolute():
    _guard_inline_content("see /opt/corp/spec.md for the API", "text",
                          (r"^/opt/corp/",))


def test_allow_path_second_nonmatching_still_blocks():
    assert "contains an absolute path" in _blocked(
        "see /opt/corp/spec.md and /tmp/other.md",
        allow=(r"^/opt/corp/",),
    )


def test_allow_path_repeatable_covers_multiple():
    _guard_inline_content("refs /opt/corp/spec.md and /opt/acme/api.md", "text",
                          (r"^/opt/corp/", r"^/opt/acme/"))


def test_allow_path_exempts_rule1_whole_value_when_absolute():
    # A whole-value absolute path matching an allow regex is exempt from Rule 1.
    _guard_inline_content("/opt/corp/spec.md", "text", (r"^/opt/corp/",))


# ─── Built-in allowed paths — endless's own config + cache dirs ──────────────

@pytest.mark.parametrize("value", [
    "~/.config/endless/endless.db",                          # Rule 1 whole value
    "the ledger DB is at ~/.config/endless/endless.db here",  # Rule 2 in prose
    "sandbox lives at ~/.cache/endless/sandboxes/e-1/endless",
])
def test_builtin_config_cache_dirs_allowed_without_allow_path(value):
    # endless's own config/cache dirs are always exempt — no --allow-path needed.
    _guard_inline_content(value, "text", ())


def test_builtin_absolute_home_expanded_form_allowed(tmp_path, monkeypatch):
    # The /Users/... (already-absolute) spelling of ~/.config/endless is exempt.
    from pathlib import Path
    cfg = Path.home() / ".config" / "endless" / "endless.db"
    _guard_inline_content(f"see {cfg} for the ledger", "text", ())


def test_builtin_honors_xdg_config_home(monkeypatch):
    # A path under $XDG_CONFIG_HOME/endless is exempt (resolution honors XDG).
    monkeypatch.setenv("XDG_CONFIG_HOME", "/tmp/fake-xdg-cfg")
    _guard_inline_content("cfg at /tmp/fake-xdg-cfg/endless/config.json here",
                          "text", ())
    # HOME-anchored ~/.config/endless stays exempt even under XDG redirection.
    _guard_inline_content("db at ~/.config/endless/endless.db here", "text", ())
    # A sibling under the XDG root but NOT under /endless still blocks.
    assert "contains an absolute path" in _blocked(
        "x at /tmp/fake-xdg-cfg/other/y here")


def test_builtin_honors_xdg_cache_home(monkeypatch):
    # A path under $XDG_CACHE_HOME/endless is exempt.
    monkeypatch.setenv("XDG_CACHE_HOME", "/tmp/fake-xdg-cache")
    _guard_inline_content("sandbox at /tmp/fake-xdg-cache/endless/sandboxes/e-1 here",
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


def test_resolve_file_content_is_never_gated(tmp_path):
    # A file that itself contains an absolute path loads fine — --<name>-file is
    # the sanctioned way to load real content.
    p = tmp_path / "plan.md"
    p.write_text("the sandbox lives at /tmp/anything and that is fine here")
    assert _resolve_content_flag(None, str(p), "text").startswith("the sandbox")
