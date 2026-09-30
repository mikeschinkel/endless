rebuild-db --confirm replaces the tasks table with a from-scratch ledger projection that currently replays to only ~714 tasks vs 947 live (FK-before-parent, duplicate tasks.id, invalid phase).

It would silently destroy ~25% of tasks; only dry-run-by-default has prevented a wipe.
