package notes

import (
	"bytes"
	"fmt"
	"mime/quotedprintable"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
)

// BuildNoteMessage produces (Message-ID, raw RFC 2822 bytes) for a
// note APPEND. originalMID is non-empty only for edits; it points at
// the very first version of the note so the chain is traversable.
func BuildNoteMessage(authorEmail, authorName, title, bodyMD, originalMID string, version int) (string, []byte, error) {
	host, _ := os.Hostname()
	if host == "" {
		host = "orbital.local"
	}
	msgID := fmt.Sprintf("<%s@%s>", uuid.NewString(), host)
	now := time.Now().UTC().Format(time.RFC1123Z)

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "From: %q <%s>\r\n", authorName, authorEmail)
	fmt.Fprintf(&buf, "To: <%s>\r\n", authorEmail)
	fmt.Fprintf(&buf, "Subject: %s\r\n", title)
	fmt.Fprintf(&buf, "Date: %s\r\n", now)
	fmt.Fprintf(&buf, "Message-ID: %s\r\n", msgID)
	fmt.Fprintf(&buf, "%s: %s\r\n", HeaderNote, NoteVersionV1)
	fmt.Fprintf(&buf, "%s: %d\r\n", HeaderNoteVersion, version)
	if originalMID != "" {
		fmt.Fprintf(&buf, "%s: %s\r\n", HeaderNoteOrigMID, originalMID)
	}
	fmt.Fprintf(&buf, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&buf, "Content-Type: %s\r\n", ContentTypeMD)
	fmt.Fprintf(&buf, "Content-Transfer-Encoding: quoted-printable\r\n")
	fmt.Fprintf(&buf, "\r\n")
	w := quotedprintable.NewWriter(&buf)
	_, _ = w.Write([]byte(bodyMD))
	_ = w.Close()

	return msgID, buf.Bytes(), nil
}

// IsNoteMessage looks for the X-Webmail-Note header in a raw RFC 2822
// header blob.
func IsNoteMessage(rawHeaders string) bool {
	return strings.Contains(rawHeaders, HeaderNote+": ")
}
