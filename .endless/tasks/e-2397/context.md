On 2026-10-18 a task worktree's branch code migrated the main database to a
version the installed endless-go did not carry (the cause is E-2396). Every
session was then refused (ED-1570). Recovery took about an hour by hand:
- picking the right backup (the pre-migration one, 2s before the upgrade);
- finding and killing the session monitors holding the database open;
- a first `db restore` that silently produced an EMPTY database: in the gap
  between parking the old file and copying the backup in, the tmux status
  line (`endless-go tmux status-line`, every 2s) opened the empty path and
  created a fresh database. The dry run cannot see openers that brief;
- blanking the status row on every Endless tmux server, then restoring again;
- landing the branch, which was the cheaper fix all along.

Mike wants this formalized into a command, because an accidental forward
migration will happen again.
