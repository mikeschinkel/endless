When a fault's `errors` table row cannot be written (database unreachable, table
missing, insert rejected, or a worktree build refusing main under ED-1601), the
occurrence is still appended to `errors.jsonl`, marked `"unindexed": true` with a
null `fault_id` and the reason in `index_error` (E-1887). `eeh` lists these as
"recorded to the log but never indexed" and prefixes each reason with
"not indexed:".

"Indexed" is a coined term for "a database row was written". It was never
approved, and it makes the reader learn a term instead of the message saying the
plain thing. It showed up while explaining an `eeh` report caused by another
session's hook probe (a junk payload fed to a worktree build under the real HOME).
