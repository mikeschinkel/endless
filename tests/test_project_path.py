"""Tests for endless.project_path — the two project-path forms and the one rule.

The Go half is pinned in internal/monitor/projects_test.go; the two agreeing
end to end is what .endless/tasks/e-2011/verify.sh drives through the real hook.
"""

import re
from pathlib import Path

import click
import pytest

from endless import db
from endless.project_path import (
    home,
    match_project_path,
    project_name_for_cwd,
    resolved,
    stored,
)
from endless.register import register_project


@pytest.fixture
def temp_home(tmp_path, monkeypatch):
    """Point $HOME at a directory the test owns, and hand back its resolved
    form. Asserting against the developer's real $HOME would pass on this
    machine and prove nothing about the rule."""
    h = tmp_path / "home"
    h.mkdir()
    monkeypatch.setenv("HOME", str(h))
    return h.resolve()


def _seed(path, name="acme", pid=None):
    cols = "name, path" if pid is None else "id, name, path"
    args = (name, str(path)) if pid is None else (pid, name, str(path))
    ph = "?, ?" if pid is None else "?, ?, ?"
    db.execute(f"INSERT INTO projects ({cols}) VALUES ({ph})", args)


# ─── resolved ───────────────────────────────────────────────────────────────

def test_resolved_resolves_symlink_components(tmp_path):
    real = (tmp_path / "real").resolve()
    real.mkdir()
    link = tmp_path / "link"
    link.symlink_to(real)

    assert resolved(link) == real
    assert resolved(link / "src") == real / "src"


def test_resolved_is_non_strict(tmp_path):
    """A directory that does not exist yet still gets its existing prefix
    resolved — the property the Go half reproduces by hand, because
    filepath.EvalSymlinks fails outright on a missing leaf."""
    real = (tmp_path / "real").resolve()
    real.mkdir()
    link = tmp_path / "link"
    link.symlink_to(real)

    assert resolved(link / "not" / "created" / "yet") == \
        real / "not" / "created" / "yet"


def test_resolved_expands_tilde():
    """The read half of the storage rule since E-2011: `~/...` is the shape the
    column normally holds, so every caller that uses the answer as a directory
    depends on this branch. Pinned because bare `Path.resolve()` treats a
    literal `~` as an ordinary directory name and returns `<cwd>/~/...`."""
    assert resolved("~/some-project") == home() / "some-project"
    assert resolved("~") == home()
    assert resolved("~/some-project") != Path("~/some-project").resolve()


def test_resolved_is_idempotent(tmp_path):
    once = resolved(tmp_path)
    assert resolved(once) == once


# ─── stored ─────────────────────────────────────────────────────────────────

def test_stored_is_home_relative(temp_home):
    """The rule itself, and the reason `endless sql` output is readable."""
    assert stored(temp_home / "Projects" / "acme") == "~/Projects/acme"


def test_stored_returns_a_str_not_a_path(temp_home):
    """The type IS the signal. `Path("~/Projects/acme")` is a relative path
    whose first component is named `~`, and every `open`, `exists` and
    `subprocess(cwd=...)` on it is quietly wrong — so the stored form never
    leaves this module as a Path."""
    assert isinstance(stored(temp_home / "Projects" / "acme"), str)
    assert isinstance(resolved(temp_home / "Projects" / "acme"), Path)


def test_stored_home_itself_is_a_bare_tilde(temp_home):
    """The boundary the prefix test cannot cover: $HOME is under $HOME."""
    assert stored(temp_home) == "~"


def test_stored_keeps_paths_outside_home_absolute(tmp_path, temp_home):
    """The mixed column ED-1562 chose deliberately: a directory with no
    home-relative spelling keeps the absolute one rather than acquiring a
    `../..` no reader could parse."""
    outside = (tmp_path / "elsewhere").resolve()
    outside.mkdir()
    assert stored(outside) == str(outside)


def test_stored_does_not_swallow_a_sibling_of_home(tmp_path, temp_home):
    """`/Users/mikey` does not live inside `/Users/mike`. Storing it as `~y`
    would point the expansion at a directory that never existed."""
    sibling = Path(str(temp_home) + "-sibling")
    sibling.mkdir()
    assert stored(sibling) == str(sibling.resolve())


def test_stored_resolves_symlinks_before_relativizing(tmp_path, temp_home):
    """E-2011 re-points E-2002's invariant, it does not weaken it: the stored
    spelling still must not depend on how the caller spelled the path."""
    real = temp_home / "Projects" / "acme"
    real.mkdir(parents=True)
    link = tmp_path / "link-to-acme"
    link.symlink_to(real)

    assert stored(link) == "~/Projects/acme"


def test_stored_round_trips_through_resolved(tmp_path, temp_home):
    """The property the whole split rests on: the two accessors are inverses,
    so nothing is lost by storing the shorter form."""
    outside = (tmp_path / "elsewhere").resolve()
    outside.mkdir()
    inside = temp_home / "Projects" / "acme"
    inside.mkdir(parents=True)

    for path in (temp_home, inside, outside):
        assert resolved(stored(path)) == path


def test_both_forms_fail_loudly_without_home(tmp_path, monkeypatch):
    """E-2011 asked for this by name. A silent answer is `<cwd>/~/Projects/acme`
    on the read side and a second spelling of an already-registered directory on
    the write side; both surface far from the cause, so both refuse.

    A path with no tilde needs no home and must still succeed — that is what
    keeps a machine with a broken $HOME able to resolve a project stored
    absolutely."""
    outside = (tmp_path / "elsewhere").resolve()
    outside.mkdir()
    monkeypatch.delenv("HOME", raising=False)

    with pytest.raises(click.ClickException):
        resolved("~/Projects/acme")
    with pytest.raises(click.ClickException):
        stored(outside)
    assert resolved(outside) == outside


# ─── match_project_path ─────────────────────────────────────────────────────

def test_match_finds_row_through_a_symlinked_path(isolated_env, tmp_path):
    real = (tmp_path / "acme").resolve()
    real.mkdir()
    _seed(real)
    link = tmp_path / "link-to-acme"
    link.symlink_to(real)

    assert match_project_path(link) == str(real)


def test_match_tolerates_a_row_stored_unresolved(isolated_env, tmp_path):
    """The legacy-database case, and the reason E-2002 needs no data migration:
    normalization at the comparison boundary makes the stored spelling
    irrelevant."""
    real = (tmp_path / "acme").resolve()
    real.mkdir()
    link = tmp_path / "link-to-acme"
    link.symlink_to(real)
    _seed(link)  # written before the rule existed

    assert match_project_path(real) == str(link)


def test_match_prefers_the_older_of_two_rows_for_one_directory(
    isolated_env, tmp_path,
):
    """A database that already caught the bug holds the real registration AND the
    auto-registered duplicate. The lower id — the row with the tasks — wins."""
    real = (tmp_path / "acme").resolve()
    real.mkdir()
    link = tmp_path / "link-to-acme"
    link.symlink_to(real)
    _seed(real, name="acme", pid=1)
    _seed(link, name="acme-2", pid=2)

    assert match_project_path(link) == str(real)


def test_match_returns_none_for_an_unregistered_directory(
    isolated_env, tmp_path,
):
    assert match_project_path(tmp_path / "nowhere") is None


# ─── project_name_for_cwd ───────────────────────────────────────────────────

def test_name_for_cwd_reads_the_on_disk_config_first(isolated_env, tmp_path):
    proj = tmp_path / "proj"
    (proj / ".endless").mkdir(parents=True)
    (proj / ".endless" / "config.json").write_text('{"name": "from-config"}')
    _seed(proj, name="from-db")

    assert project_name_for_cwd(proj) == "from-config"


def test_name_for_cwd_falls_back_to_the_registered_row(isolated_env, tmp_path):
    real = (tmp_path / "acme").resolve()
    real.mkdir()
    _seed(real, name="acme")
    link = tmp_path / "link-to-acme"
    link.symlink_to(real)

    assert project_name_for_cwd(link) == "acme"


def test_name_for_cwd_is_none_outside_any_project(isolated_env, tmp_path):
    assert project_name_for_cwd(tmp_path) is None


# ─── the write side ─────────────────────────────────────────────────────────

def test_register_stores_the_canonical_path(isolated_env, tmp_path):
    """Registering through a symlink must store the canonical path, so the Go
    hook — which normalizes the cwd Claude Code hands it — matches on the
    indexed fast path rather than the legacy scan. Outside $HOME that form is
    still absolute."""
    real = (tmp_path / "acme").resolve()
    real.mkdir()
    link = tmp_path / "link-to-acme"
    link.symlink_to(real)

    register_project(link, name="acme", infer=True)

    rows = db.query("SELECT path FROM projects WHERE name = ?", ("acme",))
    assert len(rows) == 1
    assert rows[0]["path"] == str(real)


def test_register_stores_a_project_under_home_relative(
    isolated_env, temp_home,
):
    """The E-2011 write side, end to end through the real command: the column
    holds `~/...`, which is the whole point — `endless sql` over the database is
    readable."""
    proj = temp_home / "Projects" / "acme"
    proj.mkdir(parents=True)

    register_project(proj, name="acme", infer=True)

    rows = db.query("SELECT path FROM projects WHERE name = ?", ("acme",))
    assert len(rows) == 1
    assert rows[0]["path"] == "~/Projects/acme"


def test_match_finds_a_home_relative_row_from_an_absolute_path(
    isolated_env, temp_home,
):
    """The shape every row takes after E-2011, looked up the way the CLI looks
    it up: an absolute cwd against a `~/...` column."""
    proj = temp_home / "Projects" / "acme"
    proj.mkdir(parents=True)
    _seed("~/Projects/acme")

    assert match_project_path(proj) == "~/Projects/acme"


def test_match_still_finds_a_row_left_absolute(isolated_env, temp_home):
    """The upgrade path: a database repaired by E-2002 but not yet by E-2011
    holds absolute rows, and they must keep matching — or the first event after
    the upgrade auto-registers a duplicate for every project."""
    proj = temp_home / "Projects" / "acme"
    proj.mkdir(parents=True)
    _seed(proj)

    assert match_project_path(proj) == str(proj)


# ─── the guard ──────────────────────────────────────────────────────────────

def _sql_literals(source: str) -> str:
    """Every string-literal body in `source`, concatenated.

    Analyzing SQL means looking at the SQL, not at the Python around it.
    Working on raw source went wrong twice: the first cut matched line by line
    and could not see a query split across adjacent literals, and treating the
    whole file as SQL makes `db.query("SELECT ...")` itself look like a
    parenthesized subquery. Pulling the literals out first leaves clean SQL,
    where the rest of this is straightforward.

    Comments are dropped — prose about a query is not a query. Literals are
    joined by a space, so adjacent literals forming one statement read as one
    statement; two unrelated literals may also run together, which can only
    over-report. Over-reporting costs an import; under-reporting costs a
    shipped bug.
    """
    triples = ('"' * 3, "'" * 3)
    out: list[str] = []
    i, n = 0, len(source)
    while i < n:
        ch = source[i]
        if ch == "#":
            j = source.find("\n", i)
            i = n if j < 0 else j
            continue
        if source.startswith(triples, i):
            delim = source[i:i + 3]
            j = source.find(delim, i + 3)
            if j < 0:
                break
            out.append(source[i + 3:j])
            i = j + 3
            continue
        if ch in "\"'":
            j = i + 1
            while j < n and source[j] != ch:
                j += 2 if source[j] == "\\" else 1
            out.append(source[i + 1:j])
            i = j + 1
            continue
        i += 1
    return " ".join(out)


def _strip_subqueries(sql: str) -> str:
    """Flatten parentheses, deleting any group that contains its own SELECT.

    Without this, an inline scalar subquery inside a column list — as
    `endless project list` has — hides the outer query's `FROM projects` behind
    a nearer SELECT and the outer query goes unseen.

    Innermost group first, and non-SELECT groups are unwrapped rather than
    dropped, so a subquery containing a function call (`(SELECT count(*) ...)`)
    becomes innermost in turn and is deleted whole. Matching SELECT-bearing
    groups directly would miss exactly that one, which is the shape actually in
    the tree.
    """
    group = re.compile(r"\(([^()]*)\)")
    while True:
        m = group.search(sql)
        if not m:
            return sql
        body = "" if re.search(r"\bSELECT\b", m.group(1), re.IGNORECASE) else m.group(1)
        sql = sql[:m.start()] + " " + body + " " + sql[m.end():]


def _selects_projects_path(source: str) -> str | None:
    """The first query in `source` that reads `projects.path`, else None.

    Each SELECT's window ends at the next SELECT, so a neighbouring query
    cannot lend it a `projects` it does not reference; subqueries are stripped
    first so that cut lands between statements rather than inside one.
    """
    sql = _strip_subqueries(re.sub(r"\s+", " ", _sql_literals(source)))
    starts = [m.start() for m in re.finditer(r"\bSELECT\b", sql, re.IGNORECASE)]
    for i, sel in enumerate(starts):
        end = starts[i + 1] if i + 1 < len(starts) else len(sql)
        window = sql[sel:end]
        if not re.search(r"\b(FROM|JOIN)\s+projects\b", window, re.IGNORECASE):
            continue
        head = re.split(r"\s+FROM\s+", window, maxsplit=1, flags=re.IGNORECASE)
        if len(head) < 2:
            continue
        for col in head[0][len("SELECT"):].split(","):
            col = col.strip().lower()
            if col == "path" or col.endswith(".path") or col == "*" or col.endswith(".*"):
                return window[:120].strip()
    return None


def test_every_projects_path_reader_normalizes():
    """The Python companion to Go's TestEveryProjectsPathReaderResolves.

    The tilde made `projects.path` a string that LOOKS usable: `Path(row["path"])
    / ".endless"` runs happily and yields a directory that has never existed. So
    the hazard is no longer "did you resolve symlinks", it is "did you resolve
    at all", and it lands in whichever command reads the column next.

    The rule: a module that SELECTs `projects.path` must import from
    `endless.project_path` — `resolved()` when the value touches the
    filesystem, `stored()` when it is displayed or compared. File-granular on
    purpose: pinning it tighter would mean parsing Python, and the point is to
    make the omission visible, not to prove the use is correct.
    """
    src_dir = Path(__file__).resolve().parent.parent / "src" / "endless"
    offenders = []
    for module in sorted(src_dir.glob("*.py")):
        text = module.read_text()
        if "endless.project_path" in text or module.name == "project_path.py":
            continue
        query = _selects_projects_path(text)
        if query:
            offenders.append(f"{module.name}: {query}")
    assert not offenders, (
        "these read projects.path without importing endless.project_path; "
        "a stored path is `~/...` and must go through resolved() before it "
        "touches the filesystem (E-2011):\n  " + "\n  ".join(offenders)
    )
