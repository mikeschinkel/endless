range-diff's similarity pairing, which is what catches a conflict-resolved landing, would run as a second stage over only the unmatched residue.

Deferred from E-2111 deliberately: with the verdict computed by a background job and a settled verdict surviving base movement, every miss is off the critical path and cold-miss storms are small.

Measure first; build only if misses actually hurt.

Invalidation rides the same base-tip watermark E-2111 introduces.
