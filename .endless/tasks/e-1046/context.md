E-787 Phase 4 pytest tests catch 'does the new migration apply cleanly to a fresh schema'. E-787 Phase 5 catches 'does the new migration apply cleanly to a copy of prod schema'.

These are different — fresh schemas don't carry the historical column ordering, dropped tables, or quirks accumulated over real prod migrations.

Without it, migrations can silently break against real prod schemas after passing all pytest tests.
