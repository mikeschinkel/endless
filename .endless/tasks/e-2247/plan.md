# Store cross-project links by the visibility of both ends

Decided with Mike, 2026-10-05, in the E-1883 follow-up on E-2216.

## Visibility discovery (never committed)

- A project's visibility is discovered, not declared: an unauthenticated
  HTTPS GET of its remote's URL. 200 = public; 404 (private or missing) =
  private; no remote = unknown. No `gh` dependency.
- Cache the result in the machine database with a 24h TTL. When writing a
  cross-project link and the cache entry has expired, re-check then.
- Offline or failed request: use the last cached value; none cached =
  unknown. Every failure falls back to the safe side.
- Nothing is written to `.endless/config.json`: a committed value goes stale
  when a repo's visibility changes.

## Where a cross-project link is stored

| Ends | Stored |
|---|---|
| both public | in BOTH projects' ledgers, so each rebuilds its own side alone |
| one public, one private | ONLY in the private project's ledger; the public side shows it only on machines where the private project is registered |
| either unknown | opaque reference (entity id only, no title or project name) in both ledgers, resolved only where the other project is registered |
| both private | opaque reference in both ledgers (placeholder until a permission-aware service exists) |

Visibility is evaluated when the link is written. A later visibility change
does not rewrite history; scanning/cleanup for that is separate (maybe).

## Work

1. Visibility discovery + cache table + TTL refresh.
2. Relation-add/remove event emission: resolve both ends' projects (via the
   entity → project lookup from E-2216) and their visibility; emit to the
   ledger(s) the table above selects, with opaque payloads where required.
3. Projection: a link event replayed from one project's ledger whose other
   end is not registered locally is kept as an unresolved reference, not an
   error.
4. Display: render an unresolved/opaque link as such ("linked to an item in
   another project you do not have here").

## Verification

- Link a task in endless to one in go-cfgstore (both public): the event
  appears in both ledgers; rebuilding either project alone keeps the link.
- With a private test repo: the link event appears only in the private
  project's ledger; the public project's ledger is unchanged.
- With no network and no cache: the link is stored as opaque references in
  both ledgers, with no title or project name.
