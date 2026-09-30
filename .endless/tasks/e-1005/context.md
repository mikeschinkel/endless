Observed during E-970 implementation.

Symptom: 'endless phrase add foo' succeeds, then 'endless task add' immediately fails because validate_title runs against a stale snapshot.

Workaround: 'uv tool install -e . --force'.
