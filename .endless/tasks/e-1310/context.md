internal/events/writer.go's DefaultMaxEventsPerSegment is 10000.

Current file at 1237 lines is already 1.4MB (~1.1KB/event), so 10000 would mean ~11MB segments.

JetBrains IDEs and many editors struggle past 1-2MB, and users may occasionally need to open the ledger by hand (debugging, forensic check).
