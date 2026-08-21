"""`endless session turn` — read a session's raw draft, or an A/B variant.

The one human-facing surface the minimizer's autoresearch loop adds, and it is
deliberately the smallest one that works.

E-1953's planning session designed a side-by-side RAW/MINIMIZED renderer with
column layout, table wrapping and a diff. All of that lapsed with the viewer it
was contingent on. What replaces it is this: dump the RAW draft, verbatim, no
diff and no columns. The reviewer keeps the minimized reply in the adjacent tmux
pane and compares by eye — which is what they were going to do anyway, and which
costs nothing to build and nothing to learn.

Two things about the shape that are easy to get wrong and were:

**0-based, counting back, and no negative sign.** `session turn` = `session turn
0` = the most recent. If the latest turn is #503, `session turn 3` is #500. A
negative offset (`session turn -2`) carries the same mental model but collides
with flag parsing, forcing `--` or a special case on every invocation; the
direction is already implied by "back", so the sign carries nothing.

The help text is what makes 0-based read naturally: it says "N turns back", never
"the Nth turn". Under the second wording 0-based is surprising; under the first
it is obvious. This was very nearly mis-designed as 1-based on the strength of
bad phrasing alone.

**The reviewer is reading somebody else's session.** They are in their own pane
looking at an agent's pane, so the default is the sibling Claude session in this
tmux window, not the caller. `task report --raw` cannot serve this purpose at
all: it resolves the CALLING session, so it can never reach another session's
draft.
"""

from __future__ import annotations

import shutil
import subprocess
import sys

import click

from endless import db

# The gate kind for a report row; mirrors gatekind.GateKindRelay.
_RELAY_KIND = 2


def _resolve_session_id(session_ref: str | None) -> int:
    """The session whose turns to read.

    An explicit ref goes through the DB resolver, not the live-pane matcher, so
    an ENDED session's history is still readable — the drafts outlive the pane,
    and the moment you most want one is after the session that wrote it is gone.
    """
    from endless import session_cmd
    if session_ref:
        return int(session_cmd._resolve_session(session_ref)["id"])

    project_root = session_cmd._project_root_for_cwd()
    live = session_cmd._live_sessions(project_root)
    companion = session_cmd._resolve_companion(
        None, live, list_hint="endless session list")
    return int(companion["endless_session_id"])


def _turns(session_id: int) -> list[list[dict]]:
    """The session's reported turns, newest first, each as its list of rows.

    A paired turn is ONE turn with two rows. Collapsing them here is what makes
    the offset mean what the user thinks it means: they saw one reply, so
    counting back three should skip three replies, not three variants of two.
    """
    rows = db.query(
        "SELECT id, raw_draft, sanctioned_text, emitted_text, pair_id, pair_slot, "
        "       bypassed, task_id, triggered_at "
        "FROM session_gates "
        "WHERE session_id = ? AND kind_id = ? AND raw_draft IS NOT NULL "
        "ORDER BY id DESC",
        (session_id, _RELAY_KIND),
    )
    turns: list[list[dict]] = []
    seen_pairs: set[int] = set()
    for row in rows:
        r = dict(row)
        pair = r.get("pair_id")
        if pair:
            if pair in seen_pairs:
                continue
            seen_pairs.add(pair)
            members = db.query(
                "SELECT id, raw_draft, sanctioned_text, emitted_text, pair_id, "
                "       pair_slot, bypassed, task_id, triggered_at "
                "FROM session_gates WHERE pair_id = ? ORDER BY id",
                (pair,),
            )
            turns.append([dict(m) for m in members])
            continue
        turns.append([r])
    return turns


def _emit(text: str, *, paged: bool, render: bool) -> None:
    """Print text, optionally through a pager, optionally colorized.

    A RAW draft is never rendered. Its whole purpose is to be exactly what the
    agent wrote, so passing it through a markdown renderer would defeat the one
    property it has. A VARIANT is rendered, because it is a finished reply and
    the user is reading it the way they would have received it.
    """
    body = text
    if render:
        rendered = _render_markdown(text)
        if rendered is not None:
            body = rendered.rstrip("\n")

    if not paged:
        click.echo(body)
        return

    pager = subprocess.Popen(
        ["less", "-R", "-F", "-X"], stdin=subprocess.PIPE, text=True)
    try:
        pager.stdin.write(body + "\n")
    except BrokenPipeError:
        pass
    finally:
        try:
            pager.stdin.close()
        except BrokenPipeError:
            pass
        pager.wait()


def _render_markdown(content: str) -> str | None:
    """Colorize via `endless-go markdown render`, or None on any failure."""
    from endless.event_bridge import _resolve_endless_go
    try:
        binary = _resolve_endless_go()
    except click.ClickException:
        return None
    width = shutil.get_terminal_size().columns
    try:
        result = subprocess.run(
            [binary, "markdown", "render", "--width", str(width)],
            input=content, capture_output=True, text=True, timeout=10,
        )
    except (OSError, subprocess.SubprocessError):
        return None
    if result.returncode != 0:
        return None
    return result.stdout


def session_turn(target: str | None, session_ref: str | None, paged: bool) -> None:
    """Print one turn's raw draft, or one variant of the pending turn."""
    session_id = _resolve_session_id(session_ref)
    turns = _turns(session_id)
    if not turns:
        click.echo(
            f"ES-{session_id} has no reported turns yet — nothing has gone "
            "through the minimizer.",
            err=True,
        )
        raise SystemExit(1)

    slot = (target or "").strip().upper()
    if slot in ("A", "B"):
        _emit_variant(session_id, turns[0], slot, paged)
        return

    offset = _parse_offset(target)
    if offset >= len(turns):
        click.echo(
            f"ES-{session_id} has {len(turns)} reported turn(s); "
            f"{offset} turns back does not exist.",
            err=True,
        )
        raise SystemExit(1)

    turn = turns[offset]
    _emit(turn[0].get("raw_draft") or "", paged=paged, render=False)


def _parse_offset(target: str | None) -> int:
    if target is None or target == "":
        return 0
    if not target.isdigit():
        raise click.ClickException(
            f"Not a turn: {target!r}. Pass a count of turns back (0 is the most "
            "recent), or A / B to read a variant of the pending turn."
        )
    return int(target)


def _emit_variant(session_id: int, turn: list[dict], slot: str, paged: bool) -> None:
    """Print one side of the most recent A/B pair.

    Scoped to the LATEST turn on purpose. A/B is a live question — the user is
    deciding between two replies they were just handed — so `A` addresses the
    pair in front of them. Letting it address an arbitrary past pair would need a
    second coordinate and answer a question nobody has.
    """
    match = [r for r in turn if (r.get("pair_slot") or "").upper() == slot]
    if not match:
        click.echo(
            f"The most recent turn of ES-{session_id} was not a paired "
            "minimization, so there is no option "
            f"{slot} to read.",
            err=True,
        )
        raise SystemExit(1)
    _emit(match[0].get("sanctioned_text") or "", paged=paged, render=sys.stdout.isatty() or paged)
