tests/tasks/e-1870-verify.sh section 5 asserts docs/guide/index.md's happy-path ordered list numbers contiguously 1..8 by grepping for a line-leading digit-dot.

index.md has since gained Go-template conditionals ({{if .report_gate}}, {{else}}, {{end}}) inline at the start of steps 7 and 8, so those lines no longer start with a digit and the grep sees '1 2 3 4 5 6'. The script fails on main today, unrelated to any current change — verified by running the same awk and grep against 'git show main:docs' guide index.md.

Found while running the sibling verify scripts for E-2051, which touches the same guide section.
