`./tests/tasks/e-1771-verify.sh` exits 2 during `setup()` with
"ERROR: registering temp project failed", before running a single assertion.

Confirmed pre-existing and NOT caused by E-1901: the identical failure with the
identical message reproduces from the main checkout at the same commit.

The script builds a fully isolated env (temp XDG_CONFIG_HOME + XDG_CACHE_HOME,
a fresh temp git project) and then runs:

    en project register "${PROJ}" --infer --name verify1771 --status active

which fails. The sibling script `tests/tasks/e-1880-verify.sh` performs a
near-identical setup and succeeds, so the divergence between the two setups is
the place to start.

Note: E-1901 edited two assertions in this script (the steer header moved to the
BEGIN REPORT marker, and a stale `Status: unplanned` assertion was dropped —
E-1880 had already removed `Status:` from the report output, so that assertion
was asserting the opposite of the current contract). Those edits are correct but
cannot be exercised until this setup failure is fixed.
