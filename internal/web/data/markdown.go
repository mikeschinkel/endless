package data

import (
	"bytes"

	"github.com/yuin/goldmark"
)

var md = goldmark.New()

// RenderMarkdown converts markdown text to HTML.
func RenderMarkdown(input string) string {
	var buf bytes.Buffer
	if err := md.Convert([]byte(input), &buf); err != nil {
		return input
	}
	return buf.String()
}
