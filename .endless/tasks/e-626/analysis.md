'owner' (default, it's mine), 'contrib' (PR/fork work on someone else's repo), 'vendor' (patching a dependency). Role drives behavior: contrib auto-adds .endless/ to .git/info/exclude on register, may affect enforcement level and dashboard grouping. Role is independent of status (a contrib project can be active/paused/etc).

After implementation, set h2pp role=contrib.
