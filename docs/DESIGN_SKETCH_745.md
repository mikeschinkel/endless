# Design sketch: custom notifications, send + receive

Per @jba's request on #898, moving the discussion here. This builds on the constraints @jba named:
- Per-session only (no broadcast), and
- Full integration with middleware pipeline.

Revised after feedback from @jba and @maciej-kisiel on the [v1 sketch](https://github.com/modelcontextprotocol/go-sdk/issues/745#issuecomment-4324259701).

### Goal

Allow MCP server and client to send and receive custom JSON-RPC notification methods in both directions, e.g. `notifications/claude/channel`, `ide/diffAccepted`:

1. Routed through `AddSendingMiddleware` and `AddReceivingMiddleware`
2. Symmetric across `Client` and `Server`
3. Per-session in scope

### Send side

#844 implements send side via the `x-notifications/` prefix convention @jba [proposed](https://github.com/modelcontextprotocol/go-sdk/issues/745#issuecomment-3734515436) above. `ServerSession.SendNotification(ctx, method, params)` and `ClientSession.SendNotification(ctx, method, params)` wrap the payload, prepend `x-notifications/` internally so the method-validity check in `defaultSendingMethodHandler` accepts it, and strip the prefix before writing to the wire. The `x-` prefix never appears in user-facing API or on the wire.

Thus I propose we simply accept the changes provided in #844 for send.

### Receive side

Using `x-notifications/` will not work on receive: the wire delivers whatever method the peer sent, and there is no validity check to bypass, just an unknown-method drop. We need a registration API for handlers, analogous to `AddTool()`.

#### Primary API: typed per-method registration

Mirrors `ToolHandlerFor` (`mcp/tool.go:32-57`) and reuses the existing `ServerRequest[P]` / `ClientRequest[P]` types (`mcp/shared.go:491-501`), which already carry `Session`, `Params`, and `Extra` (including meta):

```go
type NotificationHandlerFor[P Params] func(context.Context, *ServerRequest[P]) error

func AddNotificationHandler[P Params](
    s *mcp.Server,
    method string,
    handler NotificationHandlerFor[P],
) error // returns error on conflict

// Symmetric on the client, using *ClientRequest[P].
```

Behavior:

1. **Registration may happen at any time**, before or after `Server.Connect()` / `Client.Connect()`. The custom-handler registry lives on `Server` / `Client` and is protected by the existing `Server.mu` mutex (`mcp/server.go:42-57`). This is the same pattern used by `AddTool` (`mcp/server.go:238-283`).
2. **Method-name validation on registration**: reject if the method matches any entry in the static spec maps `serverMethodInfos` / `clientMethodInfos` (`mcp/server.go:1366-1389`, `mcp/client.go:898-917`), or any entry already in the custom registry. Reject method names beginning with `rpc.` per JSON-RPC 2.0. Single flat namespace; dispatch order is irrelevant.
3. **Middleware**: the handler runs through `AddReceivingMiddleware` exactly like a spec method handler, via the existing dispatch in `mcp/shared.go:151-180`.
4. **Type safety**: params are unmarshalled into `*P` automatically by the same machinery used by spec method handlers.
5. **The `x-` prefix is never user-visible.** It is purely a send-side internal artifact for routing past `defaultSendingMethodHandler`.

### Internals

1. New field on `Server` and `Client`: `customNotificationHandlers map[string]methodInfo`, accessed under the existing `Server.mu` / `Client.mu`.
2. The `methodInfo` for each entry: `unmarshalParams` typed for `*P`, `newRequest` returning `*ServerRequest[P]` (or `*ClientRequest[P]`), `handleMethod` invoking the user handler.
3. `receivingMethodInfos()` returns a merged view (under lock) of static + custom maps; the merge is a snapshot so the lock is released before dispatch.
4. The dispatch path in `mcp/shared.go:151-180` is unchanged structurally; it consults the merged registry and routes to the user handler via `AddReceivingMiddleware` like any other method.
5. **No list_changed emission for custom-handler registration.** Unlike `AddTool`, there is no spec-defined list_changed notification for "custom notification handlers." This is a deliberate departure from the `AddTool` precedent. If a future spec change adds one, the registry can adopt it then.

### Decisions resolved

1. **Per-session, no broadcast** (@jba on #898).
2. **Goes through middleware in both directions** (@jba on #898).
3. **Symmetric across Client and Server** (per the send side in #844).
4. **Typed generic API** (@jba on v1 review): "I think typed is fine for now."
5. **`x-` prefix internal/invisible** (@jba on v1 review): "The `x-` prefix should not be visible to the user."
6. **Method-name validation: any string, fail-on-conflict, reject `rpc.` prefix** (@jba and @maciej-kisiel on v1 review). Side effect: dispatch order is irrelevant because no two handlers can match the same method.
7. **Handler takes a request object, mirroring `ToolHandlerFor`** (@jba on v1 review): "The NotificationHandler must take the request object so it can access meta and possibly other fields, like ToolHandlerFor."
8. **Dynamic registration with mutex protection**, modeled on `AddTool` (@jba on v1 review pushed back on pre-`Connect`-only; @maciej-kisiel flagged the race condition with the existing `Options`-based pattern).

### Open question

**Handler removal API.** Should `Server.RemoveNotificationHandler(method) error` (and the client equivalent) be in scope for this PR? Implied by dynamic registration but not explicitly requested. Easy to add; defer if a follow-up is preferred.

### Motivating use cases

1. **Claude Code channels** ([my use-case](https://github.com/mikeschinkel/endless/blob/0b41d35bc2112129cc4bb28a55ccd50439786d4e/cmd/endless-channel/main.go#L125-L128)): Server pushes `notifications/claude/channel` to the client. Send direction only in the current implementation.
2. **Gemini IDE companion** (per #745 origin): IDE sends `ide/diffAccepted`, `ide/diffRejected`, and `ide/contextUpdate` notifications that need to be received.

Claude Channels needs send; Gemini's IDE companion needs receive; a use case for each direction.

### Plan

If this design is acceptable:

1. Open a new PR with cherry-picked changes from #844 plus the new receive-side registration API and dispatch wiring.
2. Add tests for: typed receive, middleware integration, registration before and after `Connect`, conflict rejection (against spec methods and against prior custom registrations), `rpc.`-prefix rejection, and (if scoped in) handler removal.
3. Address review feedback until acceptance and merge.

Looking forward to input on the open question and confirmation on the resolved decisions.
