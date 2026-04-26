module github.com/mikeschinkel/endless

go 1.25.0

require (
	github.com/a-h/templ v0.3.1001
	github.com/google/uuid v1.6.0
	github.com/modelcontextprotocol/go-sdk v0.3.0
	github.com/templui/templui v1.9.5
	github.com/yuin/goldmark v1.8.2
	modernc.org/sqlite v1.48.2
)

// Until upstream merges SendNotification (see go-sdk#745, #844),
// use our fork which has the x-notifications/ convention from PR #844.
replace github.com/modelcontextprotocol/go-sdk => github.com/mikeschinkel/go-mcp-sdk v0.0.0-20260419050155-6c3941ff87c9

require (
	github.com/Oudwins/tailwind-merge-go v0.2.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/jsonschema-go v0.4.2 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/oauth2 v0.35.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	modernc.org/libc v1.70.0 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)
