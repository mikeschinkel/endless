Thanks @jba and @maciej-kisiel. 

Acknowledging your requests, including mirroring `ToolHandlerFor`'s shape and reusing the existing `ServerRequest[P]` / `ClientRequest[P]` types.

### Decisions resolved 
These are what I assume have been resolved; please let me know otherwise:
1. Per-session, no broadcast (#898).
2. Goes through middleware in both directions (#898).
3. Symmetric across Client and Server (per #844 send side).
4. Typed generic API.
5. `x-` prefix internal and invisible to users.
6. Method names: any string, fail-on-conflict, reject `rpc.` prefix. Single flat namespace; dispatch order is irrelevant.
8. Handler takes a request object, mirroring `ToolHandlerFor` (reusing `ServerRequest[P]` / `ClientRequest[P]`).
9. Dynamic registration with `Server.mu` / `Client.mu` protection, modeled on `AddTool()`.

### Open question

1. Should `Server.RemoveNotificationHandler(method) error` (and the client equivalent) be in scope for this PR, or scoped to a follow-up? Dynamic registration implies the need for that but you did not explicitly request it.

### Next step
Unless you have other concerns to address, I can create a PR once the open question is answered. 
