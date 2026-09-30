Fix: use `-y S` (line adjacent to status bar) regardless of the click Y.

The X stays as the captured numeric mouse_x.

Effectively this restores E-1236`s `-x M -y S` behavior with M correctly captured at binding time.
