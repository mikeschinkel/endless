Motivation: cleanup folded into a parent task gets 'test passes, ship it' treatment from agents and quietly dies; making it its own task with an explicit relation gives it independent visibility (task active, task complete validation, etc.).

Implementation: add the relation type to canonical dep_types (column is permissive per E-958, no migration), wire into 'endless task link' / 'task add --cleans-up' / 'task show' display. Schema reuses task_deps.
