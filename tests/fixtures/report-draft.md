The short answer is yes — the parser handles nested quotes correctly, because it
tracks quote depth on a stack rather than with a boolean flag.

Working through this was genuinely subtle, and I want to walk you through how I
got there. My first instinct was that the boolean was the whole story, since that
is the pattern the rest of the file uses, and it took a while to see past it.

What reframes the decision is realizing that the flag was never the load-bearing
part. The stack was already present for bracket matching, so the change is
smaller than it looks — the thing that survives from your instinct is the shape
of the fix, just applied one layer down.

It is worth noting that this is where it gets interesting: the same stack turns
out to serve both purposes, which is a fairly elegant outcome and speaks to how
robust the original design was.

Here is the state after the change:

| Input             | Before   | After    |
|-------------------|----------|----------|
| `"a"`             | ok       | ok       |
| `"a \"b\" c"`     | broken   | ok       |
| `"a 'b' c"`       | ok       | ok       |
| `'a "b" c'`       | broken   | ok       |

The fix itself:

```go
func (p *parser) inQuote() bool {
	if len(p.stack) == 0 {
		return false
	}
	top := p.stack[len(p.stack)-1]
	return top == '"' || top == '\''
}
```

Verify with `go test ./internal/parser/ -run TestNestedQuotes`.

I also checked that nothing else regressed, and there were no stray files left
in the working tree. Let me know if you'd like me to dig into any part of this
further — happy to keep going.
