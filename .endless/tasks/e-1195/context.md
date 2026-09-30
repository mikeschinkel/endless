E-971's plan specifies an O_EXCL lock file with stored PID and kill -0 staleness check, claimed/released by SessionStart/SessionEnd hooks.

Concern raised 2026-05-06: lock files can persist beyond intended lifetime (PID recycling, SessionEnd hook failures during release).
