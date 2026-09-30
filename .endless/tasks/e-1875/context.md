The reporting contract's escape hatch — the user typing FULL STATUS licenses one unconstrained response, not a sticky mode — lives only in internal/templatecmd/templates/handoff/_close.tmpl. It appears nowhere in docs/guide/.

A session whose context is compacted loses the keyword's meaning and either ignores it or treats it as a mode switch.

Found by the E-1870 guide audit.
