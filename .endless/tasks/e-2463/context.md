Seen on 2026-10-28 in gomion while E-2454 was being spawned. The spawn prompt told the session to write its verify suite at `.endless/tasks/e-2454/verify.sh`, sourcing `.endless/tasks/_harness.sh`, and to read `.endless/tasks/CLAUDE.md` before writing it. Mike says verify.sh is deprecated and every future Endless verification uses verify.toml. Two sources still point sessions at verify.sh:

- `.endless/tasks/CLAUDE.md` doesn't exist in gomion (the endless repo has one), so the file the spawn prompt says to read is missing in projects Endless didn't create, and `project init` doesn't write it.
- `endless guide orchestration` (docs/guide/orchestration.md, and verify.sh mentions in docs/guide/reference.md) still lists verify.sh as an equal option to verify.toml (the "Manifest | verify.toml / Script | verify.sh" table, "A task carries one or the other"), and refers to "that task's verify.sh" in the document-mirror section and around land.toml. A session that reads only the guide would still choose verify.sh.

The spawn prompt template that names verify.sh is a third source and is likely worth fixing in the same pass.
