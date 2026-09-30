It is NOT a hang — measured still compiling at 60s — but the two together exhaust the package's 10-minute timeout, so a plain `go test ./internal/...` sweep never completes and ends in a panic.
