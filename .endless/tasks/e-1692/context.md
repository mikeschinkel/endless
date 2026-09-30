The shared verify-script assert_* helpers wrap commands in timeout/gtimeout, which exec a real binary and cannot run a shell convenience function (e.g. go_status).

Today the binary-vs-function rule is tribal knowledge with a false-pass failure mode.
