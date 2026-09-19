## The contract, and which half shipped

internal/monitor/session.go:389-403 records E-1898's reasoning in full. Quoting
the operative part:

    With identity as (server_uuid, address) the case cannot arise: a reissued
    pane resolves to a DIFFERENT processes row, so it never collides with the
    old binding and there is nothing to invalidate. A genuine same-server
    collision (two session identities in one pane) leaves both rows alone and
    readers order by last_activity.

Two claims. The first is true of the WRITE side and is why removing the
invalidation was safe there — a reissued %422 really does get its own processes
row. The second is a claim about READERS, and no reader implements it.

## Where it breaks

src/endless/session_cmd.py, _resolve_companion:

    siblings = [
        c for c in live
        if c.get("pane_id") in window_panes and c.get("pane_id") != my_pane
    ]

`pane_id` here is the ADDRESS STRING, matched against the address strings tmux
reports for the current window. Two sessions bound to different processes rows
that happen to share "%422" both land in `siblings`. The function then refuses
an ambiguous set rather than ordering it:

    click.echo("Multiple sibling Claude panes in this window:")
    ... raise SystemExit(1)

So the write side stopped cleaning up on the promise that the read side would
disambiguate, and the read side neither disambiguates nor orders.

## Evidence

  id    session_id     state    process_id  address  last_activity
  976   42f7dd92-...   working  91          %422     2026-08-06T05:45:41
  1152  a86f2bc3-...   idle     185         %422     2026-08-26T03:38:48

Both non-ended, both reading as %422, different processes rows. `esu` in that
window refuses. Neither row is corrupt: 976 accurately records a session that
ran on an August %422 whose tmux server is long gone.

## What NOT to do

Do not "end" the older row. It is not a live session that can be ended — it is a
record of one that already finished, and ending it via the SessionEnd hook is
simulating a lifecycle event on something that has no lifecycle left. That
treats a read bug as a data problem and destroys accurate history to do it.
(This mistake was made once already while diagnosing; it is recorded here so the
implementor does not repeat it.)

## Two shapes, either of which closes it

- Compare identity the way the writer records it: carry the processes row (or
  server_uuid) through `session-query list-live` into the companion dicts, and
  match on (server_uuid, address) instead of the bare string. This is the
  faithful reading of E-1898 — pane identity is a pair, so every comparison of
  it should be a pair.
- Or implement the half E-1898 promised: when a sibling set is ambiguous, order
  by last_activity and take the newest. Cheaper, and it also covers a genuine
  same-server collision, which the identity fix does not.

The two are not exclusive and the second is a reasonable backstop for the first.

## Blast radius

Every companion-resolving surface, not just `esu`: `session use`, `session cd`,
`session show`, `session history` with no argument — anything that calls
`_resolve_companion` with no session_ref. The failure is a hard refusal, so a
long-lived tmux server that reissues pane addresses gradually makes those
commands unusable in exactly the windows that have been open longest.

## PRODUCT

Nothing here is Endless-specific. Any project whose sessions are pinned to tmux
panes accumulates rows for panes that no longer exist, and tmux reissues
addresses on server restart. A downstream user hits this the first time they
restart their tmux server with sessions recorded — with no verify scripts and no
self-dev worktrees involved.
