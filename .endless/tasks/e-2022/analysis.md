Renaming note: this task was filed as 'decide the non-self_dev upgrade policy and whether expand-contract becomes a rule'. The expand-contract half was settled as ED-1568 (hard rule) before this task was started, leaving the compatibility-range consequence in its place. A policy differing between self_dev and non-self_dev is defensible since the modes genuinely differ, but it means the dangerous path gets the least real-world exercise -- worth weighing against the extra step a refusal costs every user after every upgrade.

## From the description

(1) A normal user has one installed binary, so the two-binary problem does not arise; on a mismatch after upgrading endless, does that binary refuse (uniform behavior, one tested path, an extra step after every upgrade) or auto-upgrade forward (no interruption, but preserves the 'any binary may migrate whatever DB it opens' property this decision exists to remove)? (2) ED-1568 made expand-contract a rule, and it only pays off if the connect check admits a compatibility RANGE rather than an exact match -- goose gives a linear version, so two schema-compatible binaries compare unequal and refuse anyway.

Binaries need to declare the range they support.

How harsh the refusal must be depends on how often a mismatch can happen at all, which is what the range determines.
