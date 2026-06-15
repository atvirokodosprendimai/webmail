package mailbox

import (
	"strings"

	"github.com/jaytaylor/html2text"
)

// MaxBodyChars caps the stored body so a runaway 5MB HTML mail doesn't
// blow up the SQLite row. The truncation marker is appended so human
// readers can spot it.
const MaxBodyChars = 65536

// ExtractBody picks the best plaintext representation of an email.
// textPart wins if present (already plaintext, just normalise). Otherwise
// htmlPart is run through html2text. The result has markdown literals
// escaped so the body doesn't accidentally re-render as bold/italics
// when piped through the markdown renderer.
func ExtractBody(textPart, htmlPart string) string {
	body := strings.TrimSpace(textPart)
	if body == "" {
		converted, err := html2text.FromString(htmlPart, html2text.Options{
			PrettyTables: false,
			OmitLinks:    false,
		})
		if err == nil {
			body = converted
		}
		body = strings.TrimSpace(body)
	}
	body = escapeMarkdownLiterals(body)
	body = collapseTrailingNewlines(body)
	if len(body) > MaxBodyChars {
		body = body[:MaxBodyChars] + "\n\n... [truncated]"
	}
	return body
}

func escapeMarkdownLiterals(s string) string {
	if s == "" {
		return s
	}
	r := strings.NewReplacer(
		"\\", "\\\\",
		"*", "\\*",
		"_", "\\_",
		"`", "\\`",
	)
	return r.Replace(s)
}

func collapseTrailingNewlines(s string) string {
	for strings.HasSuffix(s, "\n\n\n") {
		s = s[:len(s)-1]
	}
	return s
}
