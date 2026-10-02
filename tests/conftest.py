"""Shared test fixtures for Endless tests."""

import json
import os
import shutil
import sqlite3
import subprocess
import tempfile
from pathlib import Path

import pytest

# Only config at import. `endless.db` imports `endless.statuses`, which reads
# the status vocabulary from endless-go AT IMPORT — so importing it here would
# run the binary before pytest_sessionstart has built and routed the fresh one.
# Fixtures import db locally instead, as they already do the CLI modules.
from endless import config

REPO_ROOT = Path(__file__).resolve().parent.parent

# E-1921: the one endless-go every test runs, built from THIS checkout by
# pytest_sessionstart. Never `<checkout>/bin/endless-go` and never whatever is
# on PATH: both belong to whoever built or installed last, and a test that ran
# one of them is how E-1917 chased a bogus "tasks has 19 columns but 18 values"
# failure produced by a copy of main's binary.
_ENDLESS_GO_BIN: Path | None = None


def pytest_sessionstart(session):
    """Build endless-go once, before collection, and route every lookup to it.

    Before collection rather than in a fixture because collection already runs
    the binary: importing `endless.db` (and so every test module) reads the
    status vocabulary from it, and inside a self-dev worktree those readers ask
    `config.worktree_endless_go()` — `<checkout>/bin/endless-go` by absolute
    path, which no fixture can reach in time and PATH does not influence.

    A failed build ends the session. There is deliberately no fallback to
    `bin/` or PATH: a fallback is the stale binary this exists to remove, and
    testing Python against a binary that no longer matches its source is the
    bug, not an inconvenience.
    """
    global _ENDLESS_GO_BIN
    go = shutil.which("go")
    if go is None:
        pytest.exit("E-1921: `go` is not on PATH, so the suite cannot build the "
                    "endless-go it runs.", returncode=pytest.ExitCode.TESTS_FAILED)
    out_dir = Path(tempfile.mkdtemp(prefix="endless-go-test-"))
    session.config.add_cleanup(lambda: shutil.rmtree(out_dir, ignore_errors=True))
    out = out_dir / "endless-go"
    build = subprocess.run(
        [go, "build", "-o", str(out), "./cmd/endless-go"],
        cwd=REPO_ROOT, capture_output=True, text=True, check=False,
    )
    if build.returncode != 0:
        pytest.exit(f"E-1921: could not build endless-go from {REPO_ROOT}:\n"
                    f"{build.stderr}", returncode=pytest.ExitCode.TESTS_FAILED)
    _ENDLESS_GO_BIN = out

    # Every PATH lookup — event_bridge, land_conflict, the vocabulary readers'
    # fallback — finds the fresh build first. Set on the process, not per test,
    # so it already holds during collection; each test's monkeypatch restores to
    # this value, and isolated_env inherits it without listing anything.
    os.environ["PATH"] = f"{out_dir}{os.pathsep}{os.environ.get('PATH', '')}"

    # And the one lookup that bypasses PATH. Only an answer naming this
    # checkout's own bin/ is redirected; a worktree a test builds under tmp_path
    # resolves exactly as production would, so the resolver stays testable.
    checkout_bin = (REPO_ROOT / "bin" / "endless-go").resolve()
    real_worktree_endless_go = config.worktree_endless_go

    def worktree_endless_go(cwd: Path | None = None) -> Path | None:
        found = real_worktree_endless_go(cwd)
        if found is not None and found.resolve() == checkout_bin:
            return out
        return found

    config.worktree_endless_go = worktree_endless_go


@pytest.fixture(scope="session")
def endless_go_bin() -> Path:
    """The endless-go built from this checkout for this session (E-1921).

    A test that runs the binary itself takes this rather than resolving one: no
    test may find `bin/endless-go` or PATH on its own.
    """
    assert _ENDLESS_GO_BIN is not None, "pytest_sessionstart did not build endless-go"
    return _ENDLESS_GO_BIN


@pytest.fixture(autouse=True)
def isolated_env(tmp_path, monkeypatch):
    """Isolate every test from the real config/DB/filesystem.

    Sets up:
    - tmp config dir (overrides CONFIG_DIR, DB_PATH, CONFIG_FILE)
    - HOME and XDG_CONFIG_HOME pointing at a separate tmp home (so the real
      config and database are unreachable from any subprocess)
    - tmp projects root with a sample project
    - fresh DB with schema applied
    """
    from endless import db

    config_dir = tmp_path / ".config" / "endless"
    config_dir.mkdir(parents=True)

    projects_root = tmp_path / "Projects"
    projects_root.mkdir()

    # Override config module paths. db.py reads config.DB_PATH dynamically
    # (E-1429), so patching the config module is sufficient.
    monkeypatch.setattr(config, "CONFIG_DIR", config_dir)
    monkeypatch.setattr(config, "CONFIG_FILE", config_dir / "config.json")
    monkeypatch.setattr(config, "DB_PATH", config_dir / "endless.db")

    # E-1429: the test binary's cwd is this self-dev worktree, which trips the
    # --db gate. Provide an explicit resolved context (as production does via
    # --db) so get_db() and Go-subprocess threading both target the tmp DB.
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", config_dir)

    # `--no-session` sets config.NO_SESSION as a process global (E-1444). A test
    # that invokes the CLI with it would otherwise leave every later test
    # emitting as an unattributed actor — which is how test_question_cmd.py
    # broke test_report_reminder.py's claim-gated transitions when run first.
    monkeypatch.setattr(config, "NO_SESSION", False)

    # Point BOTH config roots at an isolated tmp home, so nothing — this process
    # or a Go subprocess, flagged or not — can resolve the developer's real
    # database. HOME as well as XDG, because main follows XDG_CONFIG_HOME then
    # HOME (E-2186) and a stale bin/endless-go from before E-2186 follows HOME
    # alone.
    #
    # A SEPARATE home, not tmp_path itself: the test DB above must not BE main.
    # A worktree build refuses to migrate main (E-1975), so a test DB that
    # resolved as main would never get its schema. Kept distinct, it is threaded
    # to Go as `--db-dir <config_dir>` by go_db_context_args.
    isolated_home = tmp_path / ".isolated-home"
    isolated_home.mkdir()
    monkeypatch.setenv("HOME", str(isolated_home))
    monkeypatch.setenv("XDG_CONFIG_HOME", str(isolated_home / ".config"))

    # Always auto-migrate in tests, regardless of the developer's shell setting
    # for ENDLESS_AUTO_MIGRATE. Tests need a fully migrated schema.
    monkeypatch.setenv("ENDLESS_AUTO_MIGRATE", "1")

    # E-1859: suppress automatic file-time triage. Every `task add` in the
    # suite would otherwise fan out a detached model call — slow, costly, and
    # nondeterministic (it races the assertions by mutating task status).
    # Tests that exercise the triager set this explicitly.
    monkeypatch.setenv("ENDLESS_NO_TRIAGE", "1")

    # Strip the runner's interactive shell. E-2106 deleted the last reader
    # (`task claim`'s eswt probe, which shelled out to `$SHELL -ic`), so this
    # guards nothing specific today — it stays because "no test resolves
    # anything from the developer's shell" is the property this fixture is for,
    # and the next `$SHELL` reader should inherit it rather than rediscover it.
    monkeypatch.delenv("SHELL", raising=False)

    # E-2106: point Claude's transcript home at an empty tmp dir. The resume
    # verbs now stat `<claude home>/projects/*/<uuid>.jsonl` before launching,
    # so without this every test that resolves a resume target would read the
    # DEVELOPER's real transcripts — passing or failing on which conversations
    # happen to be on that machine. A test that wants a transcript to exist
    # writes one under here.
    claude_home = tmp_path / ".claude"
    (claude_home / "projects").mkdir(parents=True)
    monkeypatch.setenv("CLAUDE_CONFIG_DIR", str(claude_home))

    # Strip TMUX env vars leaked from the runner's shell. Resolver helpers
    # branch on these and will issue real `tmux list-panes` subprocess
    # calls otherwise — which hits test fakes that don't expect them.
    # Tests that exercise tmux paths set TMUX / TMUX_PANE explicitly.
    monkeypatch.delenv("TMUX", raising=False)
    monkeypatch.delenv("TMUX_PANE", raising=False)

    # E-2125: and give the suite its own tmux socket directory, because
    # deleting the vars above only stops code that BRANCHES on them. tmux
    # itself reaches the running server through the default socket regardless,
    # so anything that shells out unconditionally still talked to the
    # developer's live tmux — and one `new-window` there opens a real window,
    # in a real session, in front of a real person.
    #
    # With TMUX unset, tmux derives its socket from TMUX_TMPDIR, so pointing
    # that at an empty per-test directory means every tmux subprocess resolves
    # to a socket with no server behind it and fails with "no server running".
    # A test that wants a server can start one there; none can reach the
    # operator's.
    tmux_tmpdir = tmp_path / "tmux"
    tmux_tmpdir.mkdir()
    monkeypatch.setenv("TMUX_TMPDIR", str(tmux_tmpdir))

    # E-1455: strip Claude Code env vars leaked from a runner shell whose
    # parent IS a Claude Code session. The env-vars-as-truth layer in
    # _current_endless_session_id branches on CLAUDECODE=1 and would
    # otherwise lazy-create a sessions row for the runner's UUID in
    # every test's isolated DB. Tests exercising the env-var path set
    # these explicitly.
    monkeypatch.delenv("CLAUDECODE", raising=False)
    monkeypatch.delenv("CLAUDE_CODE_SESSION_ID", raising=False)

    # E-1966: and the two signals `agent_env` detects a harness from (E-1962),
    # for the same reason one layer over. Anything reading agent_facing would
    # otherwise answer from the RUNNER's harness, so a test asserting the
    # agent-facing form of a message passes when a Claude
    # Code session runs pytest and fails in a bare shell — which is exactly how
    # E-1966 shipped a broken test_verb_gate assertion green. Every var the
    # detector reads belongs here, not just the one that has bitten us.
    monkeypatch.delenv("CLAUDE_CODE_ENTRYPOINT", raising=False)
    monkeypatch.delenv("__CFBundleIdentifier", raising=False)

    # E-2203: and E-2159's ambient audience, the third signal agent_facing
    # reads. An agent running `endless task verify` exports
    # ENDLESS_AUDIENCE=agent to the suite, and agent_help latches it at IMPORT,
    # so deleting the variable alone is too late: pin the latch too. Without it
    # every test that models a person reads as an agent under the runner and
    # passes in a bare shell — the same split the block above exists to close.
    monkeypatch.delenv("ENDLESS_AUDIENCE", raising=False)
    from endless import agent_help as _agent_help
    monkeypatch.setattr(_agent_help, "_AMBIENT_AUDIENCE", False)
    # The other two latches agent_facing reads are module globals a CLI
    # invocation with --agent-view / --agent sets and nothing clears, so one
    # such test would make every later test in the interpreter an agent's.
    monkeypatch.setattr(_agent_help, "_AGENT_VIEW", False)
    monkeypatch.setattr(_agent_help, "_AGENT_FORMAT", False)

    # Reset DB connection so it creates a fresh one
    monkeypatch.setattr(db, "_conn", None)

    # Write default config pointing to tmp projects root
    cfg = {
        "roots": [str(projects_root)],
        "scan_interval": 300,
        "ignore": [],
    }
    with open(config_dir / "config.json", "w") as f:
        json.dump(cfg, f)

    # Initialize DB
    db.get_db()

    yield {
        "config_dir": config_dir,
        "projects_root": projects_root,
        "db_path": config_dir / "endless.db",
    }

    # Close the connection opened during this test before monkeypatch restores
    # _conn to its prior value. Otherwise the Connection object becomes
    # unreferenced but isn't GC'd promptly, leaking the SQLite/-wal/-shm fds.
    # On macOS (default ulimit -n 256) the suite blows past the limit
    # somewhere mid-run without this.
    if db._conn is not None:
        try:
            db._conn.close()
        except sqlite3.Error:
            pass


@pytest.fixture(autouse=True)
def reset_session_choice_cache():
    """E-1402: the resolver caches a single chosen session id at module
    scope so a multi-event command only prompts once. Reset before AND
    after every test so cache state can't leak across tests."""
    from endless import task_cmd
    task_cmd._session_choice_cache = None
    yield
    task_cmd._session_choice_cache = None


@pytest.fixture(autouse=True)
def disable_haiku_verb_check(monkeypatch, request):
    """E-1264: stub out the haiku verb-check subprocess in every test.

    Tests that need to exercise the real `_check_verb_via_haiku` (e.g. to
    test subprocess handling itself) should opt out with the
    `@pytest.mark.no_haiku_stub` marker.
    """
    if request.node.get_closest_marker("no_haiku_stub"):
        return
    from endless import task_cmd
    monkeypatch.setattr(task_cmd, "_check_verb_via_haiku", lambda _word: (False, None))


@pytest.fixture(autouse=True)
def disable_description_verb_rule(monkeypatch, request):
    """E-1993: suspend the description-starts-with-the-title's-verb rule.

    Most tests file tasks with placeholder descriptions ("d", "short") because
    what the description says is not their subject. Tests of the rule itself,
    and anything asserting a real filing is accepted, opt back in with
    `@pytest.mark.description_verb_rule`.
    """
    if request.node.get_closest_marker("description_verb_rule"):
        return
    from endless import task_cmd
    monkeypatch.setattr(task_cmd, "_description_verb_problems", lambda _t, _d: [])


@pytest.fixture(autouse=True)
def stub_cli_execvp(monkeypatch):
    """E-1513: under `--db sandbox` from a self-dev worktree, cli.DBAwareGroup
    re-execs into the worktree's Python source via `uv run --directory ...
    endless`. In tests that build a synthetic worktree at <tmp>/proj and
    invoke the CLI with --db=sandbox, that re-exec would replace the test
    process and try to run uv against a directory with no pyproject.toml.

    Default stub: no-op execvp so the in-process flow continues as it did
    before E-1513. Tests verifying the re-exec gate itself install their
    own capture stub via `monkeypatch.setattr(cli.os, "execvp", ...)`
    (later-fixture-wins ordering keeps both stubs cleanly torn down).
    """
    from endless import cli
    monkeypatch.setattr(cli.os, "execvp", lambda *a, **k: None)


@pytest.fixture(autouse=True)
def stub_sandbox_reset_after_create(monkeypatch, request):
    """E-1608: worktree creation seeds the sandbox by exec'ing
    `endless-go sandbox reset`. Stub it so claim tests don't depend on
    whichever endless-go is on PATH. Tests of the call itself opt out with
    the `@pytest.mark.no_sandbox_reset_stub` marker.
    """
    if request.node.get_closest_marker("no_sandbox_reset_stub"):
        return
    from endless import sandbox_cmd
    monkeypatch.setattr(sandbox_cmd, "reset_after_create", lambda _wt: None)


@pytest.fixture(autouse=True)
def stub_current_session_id(monkeypatch, request):
    """E-1401: provide a deterministic session id so emit_event's attribution
    gate doesn't fire in tests.

    The test environment has no TMUX_PANE / sibling Claude pane / env
    binding, so the real resolver returns None — which after E-1401 makes
    every cli/hook event emission refuse. Returning a stable fake id keeps
    the test suite running as if each test were inside a real bound
    session.

    Tests that specifically exercise the gate (or the resolver) opt out
    with the `@pytest.mark.no_session_stub` marker and stub the resolver
    themselves.
    """
    if request.node.get_closest_marker("no_session_stub"):
        return
    from endless import task_cmd
    monkeypatch.setattr(
        task_cmd, "_current_endless_session_id", lambda: 1
    )


@pytest.fixture
def seeded_project_at_cwd(isolated_env, monkeypatch):
    """Chdir into a clean tmp project dir and register a project there.

    Tests that exercise functions emitting events (which call _resolve_project(None))
    need cwd to resolve to a registered project. The default cwd is the endless repo,
    whose .endless/config.json gives a name that won't be in the test DB. This
    fixture chdir's to a clean tmp dir and seeds the project record at that path.

    Initializes a git repo with one empty commit so that `git worktree add`
    succeeds in any test that triggers worktree creation (e.g. E-1216's
    auto-create-worktree on `task update --text`).
    """
    from endless import db

    proj_dir = isolated_env["projects_root"]
    monkeypatch.chdir(proj_dir)

    def _git(*args: str) -> None:
        subprocess.run(["git", *args], cwd=str(proj_dir), check=True,
                       capture_output=True)

    _git("init", "-q", "-b", "main")
    _git("config", "user.email", "test@example.com")
    _git("config", "user.name", "Test")
    _git("commit", "--allow-empty", "-q", "-m", "initial")

    db.execute(
        "INSERT INTO projects (name, path, status, created_at, updated_at) "
        "VALUES ('test', ?, 'active', datetime('now'), datetime('now'))",
        (str(proj_dir),),
    )
    return proj_dir


@pytest.fixture
def sample_project(isolated_env):
    """Create a sample project directory with .endless/config.json."""
    project_dir = isolated_env["projects_root"] / "my-project"
    project_dir.mkdir()
    endless_dir = project_dir / ".endless"
    endless_dir.mkdir()

    cfg = {
        "name": "my-project",
        "label": "My Project",
        "description": "A test project",
        "language": "go",
        "status": "active",
        "dependencies": [],
        "documents": {"rules": []},
    }
    with open(endless_dir / "config.json", "w") as f:
        json.dump(cfg, f)

    return project_dir


@pytest.fixture
def registered_project(sample_project):
    """A sample project that's also registered in the DB."""
    from endless.register import register_project
    register_project(sample_project, infer=True)
    return sample_project


@pytest.fixture
def stage_live_session(monkeypatch):
    """Stage live-session dicts and patch session_cmd._live_sessions to
    return them.

    E-1426 retired the per-session JSON companion files; readers now get
    their dicts from session_cmd._live_sessions, which shells out to the
    endless-session-query Go binary and reads the DB. Tests that used to
    write JSON files into .endless/sessions/ stage their data through
    this fixture instead, which patches _live_sessions to return a
    test-controlled list without needing the binary or DB plumbing.

    Usage:
        def test_x(stage_live_session, ...):
            stage_live_session(endless_session_id=247, pane_id="%5", ...)

    Field defaults match the historical companion shape; tests override
    only what they care about. Multiple stage_live_session(...) calls
    accumulate.
    """
    staged: list[dict] = []

    def _stage(**fields) -> dict:
        data = {
            "endless_session_id": 247,
            "harness_session_id": "f41f263e-c708-4c42-af7c-083b5be04943",
            "harness": "claude",
            "pane_id": "%53",
            "cwd": "/Users/mike/Projects/endless",
            "worktree_path": "",
            "started_at": "2026-04-29T03:51:23Z",
            "state": "working",
            "task_id": None,
            "last_activity": "2026-04-29T05:00:00",
            "summary": "",
        }
        data.update(fields)
        staged.append(data)
        return data

    def _patched(project_root, harness: str = "claude") -> list[dict]:
        return [d for d in staged if d.get("harness") == harness]

    from endless import session_cmd
    monkeypatch.setattr(session_cmd, "_live_sessions", _patched)
    return _stage


@pytest.fixture
def stage_transcript():
    """Put a Claude transcript on disk for a session UUID (E-2106).

    `isolated_env` points CLAUDE_CONFIG_DIR at an EMPTY transcript home, so by
    default every session reads as one whose transcript is gone and both resume
    verbs refuse. A test whose subject is anything else — window options, the
    clobber gate, the back-stack — calls this to say "the transcript is there",
    which is the ordinary case those tests were written against.

    The project slug is arbitrary on purpose: the lookup globs every project
    directory rather than deriving the slug, because a session that ran `/cd`
    is filed under wherever it ENDED.
    """
    def _stage(uuid: str, slug: str = "-staged-project") -> Path:
        d = Path(os.environ["CLAUDE_CONFIG_DIR"]) / "projects" / slug
        d.mkdir(parents=True, exist_ok=True)
        path = d / f"{uuid}.jsonl"
        path.write_text("{}\n")
        return path

    return _stage
