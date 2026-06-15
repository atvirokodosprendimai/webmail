// Package send builds RFC 2822 messages and ships them via SMTP. The
// raw bytes BuildMessage produces are also used for IMAP APPEND to Sent
// (and APPEND-to-Notes by the notes package).
package send

import (
	"bytes"
	"fmt"
	"mime"
	"mime/multipart"
	"net/textproto"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Command struct {
	From      string // "Display Name <email>"
	To        []string
	Cc        []string
	Subject   string
	BodyText  string
	BodyHTML  string
	ReplyTo   string // single address
	InReplyTo string // RFC Message-ID
	Refs      []string
}

// BuildMessage produces (Message-ID, raw RFC 2822 bytes). The
// Message-ID is generated locally so the send layer can insert a
// matching Ingest row immediately; the poll-back via IMAP will dedup
// against it.
func BuildMessage(cmd Command) (string, []byte, error) {
	host, _ := os.Hostname()
	if host == "" {
		host = "orbital.local"
	}
	msgID := fmt.Sprintf("<%s@%s>", uuid.NewString(), host)
	now := time.Now().UTC().Format(time.RFC1123Z)

	hdr := textproto.MIMEHeader{}
	hdr.Set("From", cmd.From)
	if len(cmd.To) > 0 {
		hdr.Set("To", strings.Join(cmd.To, ", "))
	}
	if len(cmd.Cc) > 0 {
		hdr.Set("Cc", strings.Join(cmd.Cc, ", "))
	}
	hdr.Set("Subject", encodeSubject(cmd.Subject))
	hdr.Set("Date", now)
	hdr.Set("Message-ID", msgID)
	if cmd.ReplyTo != "" {
		hdr.Set("Reply-To", cmd.ReplyTo)
	}
	if cmd.InReplyTo != "" {
		hdr.Set("In-Reply-To", wrapMsgID(cmd.InReplyTo))
	}
	if len(cmd.Refs) > 0 {
		refs := make([]string, 0, len(cmd.Refs))
		for _, r := range cmd.Refs {
			refs = append(refs, wrapMsgID(r))
		}
		hdr.Set("References", strings.Join(refs, " "))
	}
	hdr.Set("MIME-Version", "1.0")

	var body bytes.Buffer
	if cmd.BodyHTML == "" {
		hdr.Set("Content-Type", "text/plain; charset=utf-8")
		hdr.Set("Content-Transfer-Encoding", "8bit")
		body.WriteString(cmd.BodyText)
	} else {
		mw := multipart.NewWriter(&body)
		hdr.Set("Content-Type", `multipart/alternative; boundary="`+mw.Boundary()+`"`)
		// text/plain
		ph := textproto.MIMEHeader{}
		ph.Set("Content-Type", "text/plain; charset=utf-8")
		ph.Set("Content-Transfer-Encoding", "8bit")
		pw, _ := mw.CreatePart(ph)
		_, _ = pw.Write([]byte(cmd.BodyText))
		// text/html
		hh := textproto.MIMEHeader{}
		hh.Set("Content-Type", "text/html; charset=utf-8")
		hh.Set("Content-Transfer-Encoding", "8bit")
		hpw, _ := mw.CreatePart(hh)
		_, _ = hpw.Write([]byte(cmd.BodyHTML))
		_ = mw.Close()
	}

	var raw bytes.Buffer
	for k, vs := range hdr {
		for _, v := range vs {
			raw.WriteString(k)
			raw.WriteString(": ")
			raw.WriteString(v)
			raw.WriteString("\r\n")
		}
	}
	raw.WriteString("\r\n")
	raw.Write(body.Bytes())
	return msgID, raw.Bytes(), nil
}

func encodeSubject(s string) string {
	for _, r := range s {
		if r > 127 {
			return mime.QEncoding.Encode("utf-8", s)
		}
	}
	return s
}

// wrapMsgID ensures a Message-ID is in canonical RFC 5322 form
// `<localpart@domain>`. go-imap/v2 returns envelope MessageID without
// the angle brackets; setting `In-Reply-To: foo@bar` instead of
// `In-Reply-To: <foo@bar>` makes most mail clients refuse to thread
// the reply. Idempotent on already-wrapped input.
func wrapMsgID(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	if strings.HasPrefix(s, "<") && strings.HasSuffix(s, ">") {
		return s
	}
	return "<" + s + ">"
}

// QuoteBody returns a reply-style quoted version of the original body.
// Mirrors what most mail clients do: "On <date>, <name> wrote:" then
// the body prefixed with "> " line-by-line. Used by the threadReply
// handler to pre-fill the reply textarea so the recipient sees thread
// context.
func QuoteBody(when time.Time, fromDisplay, body string) string {
	var b strings.Builder
	b.WriteString("\n\nOn ")
	b.WriteString(when.Format("Mon, 02 Jan 2006 at 15:04"))
	b.WriteString(", ")
	if fromDisplay != "" {
		b.WriteString(fromDisplay)
	} else {
		b.WriteString("they")
	}
	b.WriteString(" wrote:\n")
	for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		b.WriteString("> ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}
