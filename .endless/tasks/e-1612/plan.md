# E-1612 — research scope

Investigate the verification blind spot observed while writing the E-1572 ad-hoc verify script
(`tests/tasks/e-1572-verify.sh`): the combination of (a) advisory code that swallows its own
failures and (b) dependency resolution that can silently pick a stale artifact, which together
let a verify suite report ALL PASSED while the feature is actually broken.

Characterize the hazard precisely, then recommend concrete mitigations the E-1596
verification-suite framework should adopt. Deliver findings + recommendations in this task's
**outcome**.
