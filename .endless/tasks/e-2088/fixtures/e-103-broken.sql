-- Fixture for E-2088's verification suite: a change that half-succeeds.
--
-- The CREATE is valid and the SELECT is not, so applying this file must leave
-- NOTHING behind — no table, no marker. A change whose effects landed and whose
-- marker did not, or the reverse, is a database no re-run can heal, and the
-- re-runnability of a failed land rests entirely on that not happening.
--
-- The name is in the reserved 1-199 documentation band.

CREATE TABLE half_applied (
    id INTEGER PRIMARY KEY
);

SELECT * FROM a_table_that_does_not_exist;
