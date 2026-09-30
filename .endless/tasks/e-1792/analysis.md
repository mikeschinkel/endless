Per the E-1789 synthesis, the driver executes runs in Go and is the sole execution path, so RenderRunScript's pure-sh emission (internal/verify/runscript.go) is a divergent second run-loop that must be dropped.

'No Endless present' means no endless executable, never no Go executable.
