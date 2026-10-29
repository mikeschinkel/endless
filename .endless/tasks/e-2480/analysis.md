Verified 2026-10-29 with a scratch -modfile: setting only `go 1.27.0` in endless's go.mod builds endless against both go-cfgstore's main checkout and E-2412's branch, with go.sum unchanged. So `go mod tidy` is unnecessary, and skipping it keeps the change to one line instead of letting tidy pick up requires from uncommitted work in the local go-pkgs checkouts.

endless is a single module with no CI workflow and no GOEXPERIMENT setting, so nothing else names a Go version.

A main module may declare a newer go line than its dependencies, so the bump works before E-2412 lands. Landing it first avoids a window in which endless does not build.
