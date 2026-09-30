Atomic combo task update X --phase next --parent E-Y remains legal (promote+parent in one shot is fine).

Rationale: maybe = uncommitted; parent-child = scope binding; mixing creates phantom scope — a parent that appears done while uncommitted commitments linger inside.

Hard rejection, no --force (gates not guardrails).

Includes a one-shot survey command listing existing pre-rule violations so the owner can convert each to relates_to or leave as standalone.
