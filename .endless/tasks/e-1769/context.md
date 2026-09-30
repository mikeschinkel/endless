`endless session status` (the endless-go session-status renderer) is a display view that leads each row with a ✎ pencil glyph (U+270E) and truncates titles with a … ellipsis (U+2026), plus tree/box-drawing glyphs.

Those multibyte UTF-8 chars garble when the output is copied as plain text through editors (observed: pasting via pbcopy→nano made task IDs look wrong and they got hand-corrupted).
