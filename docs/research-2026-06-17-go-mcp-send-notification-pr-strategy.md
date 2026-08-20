# Playbook: Landing the Go SDK `SendNotification` PR Upstream

This document collects the recommendations and clarifications developed during our conversation about how to unblock custom JSON-RPC notifications in the official Go MCP SDK so that an Endless-style Claude Code channel plugin can be built cleanly in Go. It covers the research on the in-flight upstream work, the reasoning for driving the PR yourself rather than waiting on other contributors, the mechanics of "picking up" a PR on GitHub (which is not a literal GitHub feature but a family of practical workflows), the fork strategy for using the change in your own project before it merges, and the specific comments worth posting to move the upstream discussion forward. It is intended to be read in order the first time and then kept as a reference while you execute on it.

## Fetching the upstream context yourself

Before taking any action on the upstream repo, it is worth pulling the full text of the relevant issues and PRs yourself rather than relying on summaries. The GitHub `gh` CLI does this cleanly. The important flag is `--comments`, which includes the discussion threads — without it, you only see the opening post, which for issues like #745 is a small fraction of the real content. The diff commands let you see exactly what each PR proposes to change, which matters here because the gap between #844 and #898 is architectural and only becomes obvious when you read the code.

```bash
# The four issues and PRs of primary interest. --comments is important —
# it's where the real discussion happens.
gh issue view 706 --repo modelcontextprotocol/go-sdk --comments
gh issue view 745 --repo modelcontextprotocol/go-sdk --comments
gh pr view 844  --repo modelcontextprotocol/go-sdk --comments
gh pr view 898  --repo modelcontextprotocol/go-sdk --comments

# The actual code diffs — both PRs are small, so the diff is fast to read
# and tells you the API shape each submitter chose.
gh pr diff 844 --repo modelcontextprotocol/go-sdk
gh pr diff 898 --repo modelcontextprotocol/go-sdk

# Proactive search for related work that might have been missed.
gh issue list --repo modelcontextprotocol/go-sdk --search "notification OR Notify experimental" --state all
gh pr list    --repo modelcontextprotocol/go-sdk --search "notification OR Notify experimental" --state all
```

## Why taking this on yourself is a good move

The case for driving the PR yourself rests on three observations. First, andylbrummer's #898 has been silent for three days after the maintainer jba effectively told him his implementation missed the architectural point — silence after substantive review feedback is the normal signature of a PR that is either about to get a major revision or get abandoned. Either way, waiting indefinitely for a contributor who may not come back is the weaker play. Second, ajuijas's #844 is the architecturally correct PR but was written by someone who stated they had no use case and would welcome a handoff — so the question of whether you're stepping on anyone's toes has already been answered in public by ajuijas themselves. Third, the professional value of a merged PR to `modelcontextprotocol/go-sdk` is real: the SDK is maintained in collaboration with Google, jba is a Go team member with deep credentials from his work on gopls's jsonrpc2 implementation, and a visible contribution to this particular SDK is the kind of credential that carries weight. Combined with the fact that your use case (Claude Code channels, dogfooded through Endless) is both concrete and topical, you have a better motivation story than most contributors who come through.

There is one caution, though, which is important enough to state clearly: read #745 end-to-end before writing any code. jba's terse "(No, you're not)" response to #898 tells you that something specific was missed, and you don't want to repeat the same mistake on a second attempt. The `gh` output reveals what was missed — #898 bypasses middleware and doesn't implement jba's proposed `x-notifications/` prefix convention. Understanding why those two things matter architecturally is what distinguishes a PR that gets a polite rejection from one that gets reviewed seriously.

## Which PR to target, and why

Initially, it looked like the natural target was andylbrummer's #898, because his PR was motivated explicitly by Claude Code channels and matches your use case directly. Reading the gh output changes that conclusion. The important distinction is between #898 and #844 as architecturally different approaches, not as competing attempts at the same design.

PR #898 adds a `Notify(ctx, method, params)` method on `ServerSession` that checks for the `notifications/` prefix and then calls `ss.getConn().Notify()` directly. It is server-only — no symmetric client-side method — and it bypasses the SDK's middleware pipeline entirely. jba's objection to this approach in the #745 thread was explicit: *"All other notifications go through sending middleware. These should too."* Middleware in this SDK does accounting, logging, PII scrubbing, and other cross-cutting work, and creating a class of messages that skip it silently would be a real observability hole.

PR #844 implements the specific convention jba proposed in #745. The convention is: if a method name begins with the segment `x-notifications`, strip that segment, and send the remainder as a notification on the wire. The `x-` prefix signals "custom extension" to the middleware layer, the stripping rule means the wire-format method name is preserved exactly as the extension defines it, and the whole thing stays inside the middleware pipeline. #844 implements `SendNotification(ctx, method, params)` symmetrically on both `ClientSession` and `ServerSession`, wraps arbitrary payloads in a `customNotificationParams` type that satisfies the SDK's internal `Params` interface, prepends `x-notifications/` to the method name, routes through the existing `handleNotify` function so middleware runs, and adds a short-circuit in `defaultSendingMethodHandler` that detects the prefix, strips it, and calls `getConn().Notify()` with the real method name. Tests cover both directions.

This is the PR jba asked for in the design discussion. Rallying behind #844 rather than #898 means you are aligned with the maintainer's stated design from the start, and the work remaining is finishing what ajuijas began rather than rebuilding what andylbrummer wrote.

## Fork strategy: fork upstream directly, not the fork

The phrasing "fork the fork" came up as a possibility — meaning, use your own fork with standardbeagle's (andylbrummer's) changes already in it rather than depending on standardbeagle's fork directly in `go.mod`. The clarification is worth stating explicitly because the GitHub "fork" feature and the practical act of maintaining a modified codebase are not quite the same thing.

If you use GitHub's fork button on `standardbeagle/go-sdk`, you inherit that repository's history as your base, and you end up with two upstreams to reconcile forever — `modelcontextprotocol/go-sdk` for the flow of upstream changes, and `standardbeagle/go-sdk` for the patch branch. If standardbeagle stops syncing with upstream (and forks of abandoned PRs usually do), your fork degrades along with theirs, and every merge conflict requires you to untangle both streams. You gain nothing in exchange for that coupling because the patch itself is small.

The cleaner move is to fork `modelcontextprotocol/go-sdk` directly. You then apply the change yourself — either by cherry-picking ajuijas's commit from their branch, or by writing your own implementation that follows the same design. Your Endless `go.mod` gets a replace directive pointing at your fork:

```go
// Until upstream merges the SendNotification method (see #745, #844),
// use our fork, which contains the x-notifications/ convention implementation.
replace github.com/modelcontextprotocol/go-sdk => github.com/mikeschinkel/go-sdk v0.0.0-20260417120000-abcdef012345
```

When upstream merges, you drop the replace directive and bump your dependency to whichever release contains the merged change. One upstream to track, one authorship story on your commits, and a clean exit. If you want to credit ajuijas's work on the commit you apply — which you should — the git `--author` flag and the `Co-authored-by:` trailer described later in this document let you do that without either obscuring your role as the integrator or falsely claiming ajuijas's work as your own.

Now — as a separate and parallel activity from using the fork in Endless — you drive the upstream PR. The two don't have to be coupled. You can be running on your fork internally while simultaneously working with ajuijas (or alone) to land #844 or a successor upstream.

## The social plan: who to comment to, and in what order

The highest-leverage first move is a comment on #844 directed at ajuijas, because ajuijas has already publicly stated they'd welcome someone moving the PR forward. This is not presumptuous — it is accepting an offered baton. A secondary, shorter comment on #898 signals to andylbrummer that you've looked at his work but are focusing your effort elsewhere, which is a courtesy that costs nothing. A third comment on #745 itself, directed at the maintainers, asks the concrete design question that would unstick ratification. You already left one comment on #745 establishing your Claude Code channels use case, so the maintainers have seen your motivation; the follow-up is about procedure.

### Draft comment for PR #844

> Hi @ajuijas — thanks for putting this together. I have a concrete use case: I'm building a Claude Code channel plugin (Endless project) in Go, which needs exactly the `notifications/claude/channel` flavor of custom notification discussed in #745. Your implementation matches @jba's `x-notifications/` convention and routes through middleware, which is the design jba pushed for in the #745 thread.
>
> You mentioned above that you don't have a personal use case and would welcome someone moving this forward. I'd like to help drive it toward merge. Happy to either (a) pair with you on addressing any remaining feedback, or (b) pick it up and carry it forward if you'd rather step back — whichever works for you. Either way, full credit to you for the design and initial implementation.
>
> @jba @findleyr — is there anything specific you'd want addressed beyond what's in this PR to get to "ready for work"? The one design question I see still open is multi-session broadcast (jba's third point in #745) — do you want that solved here, or scoped to a follow-up?

This comment is designed to do several things at once. It affirms ajuijas's authorship publicly so they don't feel elbowed aside. It names your specific use case, tying the SDK work to a visible product in the Anthropic ecosystem. It offers two operational modes explicitly — collaborate or take over — and lets ajuijas pick. And it ends with a direct, well-formed question to the two maintainers that invites them to either ratify the design or name what is missing, which is the unblock you actually need.

### Draft comment for PR #898

> Hi @andylbrummer — I had a near-identical motivation here (Claude Code channels), so I read this PR carefully. I've commented on #844 offering to help drive that one forward, since it implements the `x-notifications/` convention @jba outlined in #745 and goes through the middleware pipeline, which appears to be the sticking point. If you'd rather revise this PR to align with that approach, happy to coordinate — otherwise I'll focus on #844. No preference on attribution; just want the capability to land.

This is deliberately short. The architectural gap between #898 and what jba wants is real and substantial, and the tactful move is to signal where you're putting your effort rather than trying to salvage work that would need a substantial redo.

### Framing the question to jba

You mentioned the possibility of asking jba directly for guidance on creating a PR that would be accepted. Given the state of the discussion, the more useful framing is not "what should a good PR look like" — he has already seen one in #844 and not rejected it — but "what specifically is blocking ratification of the #745 design, and what would make you comfortable merging #844 (or a revision of it)." That shifts the ask from open-ended teaching to a concrete, answerable question.

The multi-session broadcast question is the real unresolved design point. jba raised it in the #745 thread: *"A server may support many sessions. Presumably the notification should be sent to them all. (Even if there's ever only one for the IDE; we need to think more generally.)"* Neither #844 nor #898 addresses it. Naming this directly — "do you want broadcast solved in this PR, or scoped to a follow-up helper like `Server.NotifyAll`?" — gives jba a clean question to answer and is the most likely single thing to unstick the design. This question fits naturally at the end of the #844 comment above, so you may not need a separate post on #745 at all.

If ajuijas doesn't respond within a reasonable window (say a week), and if jba doesn't engage in that same window, a follow-up comment on #745 that says "I'm planning to submit a revised PR based on #844 that addresses the multi-session question by [proposed approach]; please flag any concerns before I start" is the escalation path. It signals action without being demanding and gives the maintainers a last chance to redirect before you commit effort.

## How PR "hand-off" actually works on GitHub

"Hand-off" is shorthand for a family of workflows, not a literal GitHub feature — there is no button that transfers ownership of a PR from one person to another. Understanding what actually happens under the hood matters because the comment you post should match the mechanical path you're asking ajuijas to take.

The underlying model is straightforward. A pull request is a request to merge one branch into another. The branch being offered lives on the submitter's fork of the repository — in this case, `ajuijas:feature/send-notification`, which means the `feature/send-notification` branch on ajuijas's fork of `modelcontextprotocol/go-sdk`. The PR stays open as long as that branch exists and hasn't been merged or closed. GitHub assigns the PR a number (here, #844) scoped to the upstream repo, but the number is just a label on the request; the substance of the PR is whatever commits are currently on that branch. So the question "who controls the PR" reduces to "who can push commits to the source branch," because pushing is the only thing that can change what #844 proposes.

Three realistic paths exist, in order of cleanliness.

The most literal form of hand-off is for ajuijas to add you as a collaborator on their fork. This is a five-second action in GitHub's settings, under "Collaborators and teams" on the repository. Once you accept the invitation, you can `git push` directly to the `feature/send-notification` branch. Any commits you add appear on #844 immediately, the PR number and review history stay intact, and git's commit-authorship metadata handles attribution automatically — ajuijas's original commits remain authored by ajuijas, your new commits are authored by you, and GitHub's UI shows both of you as contributors. This is the version you're asking for when the draft comment says "pair with you on addressing any remaining feedback."

The second path works if ajuijas stays minimally engaged or doesn't respond but also doesn't actively object. You open your own PR based on their branch. The git mechanics look like this: clone the upstream repo, add ajuijas's fork as a remote (`git remote add ajuijas https://github.com/ajuijas/go-sdk.git`), fetch it, check out a new branch that starts from theirs (`git checkout -b feat-send-notification-v2 ajuijas/feature/send-notification`), add whatever the review feedback asks for, push to your own fork, and open a fresh PR on upstream. Because the branch was pulled from ajuijas's repository, their existing commits retain their authorship automatically — git stores "author" and "committer" as separate fields, so ajuijas's name stays on the commits they wrote even though you're the one pushing. Your PR description cites #844 and credits ajuijas explicitly in prose. Their old PR either stays open until upstream maintainers close it as superseded, or ajuijas voluntarily closes it. This is the version you're asking for when the draft comment says "pick it up and carry it forward if you'd rather step back." The cost of this path is a new PR number and a small amount of review-discussion fragmentation.

The third path — ajuijas stays engaged and relays your patches through their own pushes — exists in theory but is rarely worth it in practice. If someone is engaged enough to relay patches, they're engaged enough to add you as a collaborator.

There is also a GitHub feature called "Allow edits by maintainers," which appears as a checkbox when a PR is opened. It is worth knowing about so you don't confuse it with collaborator access, but it does not help here. It grants push access on the PR's source branch specifically to maintainers of the upstream repository — so jba or findleyr could push fixes to ajuijas's branch if ajuijas checked the box, but you cannot, because you aren't an upstream maintainer.

## Attribution mechanics

One more tool in the attribution toolkit is worth knowing about regardless of which path gets taken: the `Co-authored-by:` trailer. In any commit message, adding a line like `Co-authored-by: Name <email@example.com>` causes GitHub to display that person as a contributor on the commit's attribution, complete with avatar. This matters in scenarios where commits get squashed or rebased during review. If upstream asks for a squash-merge of the whole PR into a single commit, for example, you can put `Co-authored-by: ajuijas <their-email>` in the squashed commit message so ajuijas's name appears on the merged commit even though the literal `git commit --author` is you. This is the standard mechanism for preserving credit across history rewrites, and it is why pair-programmed commits on GitHub often show two avatars.

The same trailer is worth using on the commit you add to your own fork when you cherry-pick or reimplement ajuijas's work for use in Endless. Doing so means your fork's history honestly reflects that the design and initial code came from ajuijas, even though you are the one maintaining the fork and shipping it in Endless.

## Why the maintainer-alignment framing matters

A small observation about the upstream process that helps explain why the "rally behind #844" strategy is high-leverage: the `modelcontextprotocol/go-sdk` repo uses a lightweight proposal process with labels like `proposal`, `proposal-accepted`, and `ready for work` (visible in issues #706, #725, and #745). The `ready for work` label is the maintainers' signal that the design has been ratified and implementation is welcome; its absence on #745 is what maciej-kisiel was pointing out when a drive-by contributor tried to implement #844 — not that the contributor was wrong, just that the design hadn't been formally blessed yet. Your lever here is that the design is ninety percent ratified in the discussion; what's missing is either the multi-session broadcast resolution or simply a maintainer being willing to stamp the label. A direct, concrete question about the one remaining design point is more likely to trigger label movement than a general "can someone look at this?" request.

The Claude Code channels use case adds something the Gemini CLI use case alone couldn't: a second independent, non-speculative motivation for the feature. Maintainers rightly weigh features with multiple real use cases more heavily than features driven by a single user. This doesn't require salesmanship — simply showing up with a concrete plugin you're building and a clear technical need is sufficient.

## Summary of the plan

The short version of all of the above: fork upstream directly, apply the change from #844 (either by cherry-pick or reimplementation), use a `go.mod` replace directive to run Endless against your fork, and in parallel comment on #844 offering collaboration or takeover to ajuijas while asking jba and findleyr about multi-session broadcast. If ajuijas collaborates, you work on their branch via GitHub collaborator access and #844 itself lands. If ajuijas steps back, you open a fresh PR based on their branch with credit to them. Either way, the upstream change lands, you drop the replace directive, and Endless continues on mainline releases. The total effort is modest, the attribution story is clean, and the result is both a shipped capability in Endless and a credible contribution to an important Go project.
