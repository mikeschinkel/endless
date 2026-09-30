Python tests resolve endless-go five different ways and only one of them builds it, so just test can silently exercise a stale binary —

in E-1917 that was a copy of main's, producing a bogus 'tasks has 19 columns but 18 values' failure.

Measured first: 5 tests fail outright without the binary, 2 skip silently, and a stale-but-present binary is invisible to both.
