Status is the only vocabulary in the system with no owning package — task type, session kind, and session-task relation each have one, and tasktype even carries String()/Label(). Status is bare string literals across ~15 sites in two languages: whole-vocabulary copies, policy subsets, lists embedded in SQL string literals, and per-status glyph/label/derivation ordering.

The failure mode is omission, not typos: E-1648 added 'submitted' and missed two sites (both still broken today), and E-1845 had to hand-edit every site for 'untriaged' with nothing to catch a miss.
