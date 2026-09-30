destroy then can't keep up with the leaked writers, leaving stale dirs that block re-entry.

Discovered during E-1074 verify on 2026-05-02: a leaked enter from 02:52AM kept recreating files in ~/.cache/endless/sandboxes/probe/ for hours, surviving multiple destroys.
