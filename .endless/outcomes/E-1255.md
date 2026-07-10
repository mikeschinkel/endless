Dropped numeric mouse_y from the right-click Y position; use `-y S` instead, which anchors the menu adjacent to the status line where the click actually was. X stays as the captured numeric mouseX.

Restores E-1236's effective placement (`-x M -y S`) with the M correctly captured at binding time so it survives the run-shell indirection introduced in E-1246.

Verification deferred to land+apply: right-click on row-0 status-right should now anchor at the cursor column AND vertically near the status bar (not top of screen).