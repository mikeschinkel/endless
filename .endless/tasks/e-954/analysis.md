Two viable paths: (a) reimplement the Go merge semantics in Python config.py (gross duplication, fragile); (b) move Python to call a Go shim like 'endless config get <key>' that runs Go's loader and returns the merged value.

Path (b) aligns with the broader E-799 / E-894 direction of Go owning data reads.

Discuss before implementing.
