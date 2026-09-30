On 0 or 2+ matches, still returns None — callers that need policy refusals (claim_item, bind_item) keep their explicit _find_sibling_claude_session call for that.

emit_event simplifies back to a single resolver call (the inline 3-layer from E-1287 becomes redundant).

release_item gets the fix for free.
