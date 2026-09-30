Reaper should treat that error string as a no-op, attempt rmdir to clean up the leftover, and continue; only true failures should surface.
