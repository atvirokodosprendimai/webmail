package mailbox

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime/quotedprintable"
	"strings"

	gmcharset "github.com/emersion/go-message/charset"
)

// decodeTextBody converts raw IMAP body bytes for a text/* part into
// UTF-8 text. It chains the right Content-Transfer-Encoding reader
// (quoted-printable / base64 / passthrough) with go-message/charset for
// the charset transcode. Errors during transcode degrade to the raw
// input — search still gets SOMETHING even on weird emails — so callers
// never get empty bodies just because the decoding pipeline hiccupped.
func decodeTextBody(raw []byte, encoding, charset string) string {
	if len(raw) == 0 {
		return ""
	}
	var r io.Reader = bytes.NewReader(raw)

	enc := strings.ToLower(strings.TrimSpace(encoding))
	// Empty / "7bit" / "8bit" / "binary" → no transfer-decode declared.
	// Some IMAP servers omit Content-Transfer-Encoding on BODYSTRUCTURE
	// even when the part on the wire is base64-wrapped (Microsoft 365 on
	// auto-forward) or quoted-printable (Outlook RE: chains on accented
	// text — Lithuanian, German etc.). Sniff the bytes: base64 first
	// (mod-4 alphabet check) then QP (=XX hex). Wrong sniff is harmless;
	// both decoders pass non-matching bytes through unchanged.
	if enc == "" || enc == "7bit" || enc == "8bit" || enc == "binary" {
		switch {
		case looksLikeBase64(raw):
			enc = "base64"
		case looksLikeQuotedPrintable(raw):
			enc = "quoted-printable"
		}
	}
	switch enc {
	case "quoted-printable":
		r = quotedprintable.NewReader(r)
	case "base64":
		r = base64.NewDecoder(base64.StdEncoding, bytes.NewReader(stripWhitespace(raw)))
	}

	if cs := strings.ToLower(strings.TrimSpace(charset)); cs != "" && cs != "utf-8" && cs != "us-ascii" && cs != "ascii" {
		if cr, err := gmcharset.Reader(charset, r); err == nil {
			r = cr
		}
	}

	out, err := io.ReadAll(r)
	if err != nil || len(out) == 0 {
		return string(raw)
	}
	return string(out)
}

func stripWhitespace(raw []byte) []byte {
	out := make([]byte, 0, len(raw))
	for _, b := range raw {
		if b == '\r' || b == '\n' || b == ' ' || b == '\t' {
			continue
		}
		out = append(out, b)
	}
	return out
}

// decodeAttachmentBytes returns the decoded binary representation of a
// MIME part's raw IMAP bytes. Attachments arrive over the wire in
// whatever Content-Transfer-Encoding the sender picked — overwhelmingly
// base64 for binaries (PDF, image, zip). Saving raw bytes produced
// "corrupted" downloads: the file was the base64 text envelope, not the
// actual binary.
func decodeAttachmentBytes(raw []byte, encoding string) []byte {
	if len(raw) == 0 {
		return raw
	}
	enc := strings.ToLower(strings.TrimSpace(encoding))
	switch enc {
	case "base64":
		decoded, err := base64.StdEncoding.DecodeString(string(stripWhitespace(raw)))
		if err == nil {
			return decoded
		}
		return raw
	case "quoted-printable":
		r := quotedprintable.NewReader(bytes.NewReader(raw))
		if decoded, err := io.ReadAll(r); err == nil {
			return decoded
		}
		return raw
	case "":
		// Legacy / sender-omitted CTE — try base64 sniff.
		if looksLikeBase64(raw) {
			if decoded, err := base64.StdEncoding.DecodeString(string(stripWhitespace(raw))); err == nil {
				return decoded
			}
		}
		return raw
	}
	return raw
}

// looksLikeQuotedPrintable returns true when the byte stream contains at
// least one well-formed `=XX` hex escape — a strong tell that the body
// is quoted-printable even when BODYSTRUCTURE doesn't declare it.
func looksLikeQuotedPrintable(raw []byte) bool {
	for i := 0; i+2 < len(raw); i++ {
		if raw[i] != '=' {
			continue
		}
		if isHex(raw[i+1]) && isHex(raw[i+2]) {
			return true
		}
	}
	return false
}

func isHex(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'A' && b <= 'F') || (b >= 'a' && b <= 'f')
}

// looksLikeBase64 cheaply checks whether the byte stream is plausible
// base64: every non-whitespace byte must be from the alphabet, total
// non-whitespace bytes must be at least one block and a multiple of 4.
func looksLikeBase64(raw []byte) bool {
	if len(raw) < 8 {
		return false
	}
	nonWS := 0
	for _, b := range raw {
		if b == '\r' || b == '\n' || b == ' ' || b == '\t' {
			continue
		}
		if (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') || b == '+' || b == '/' || b == '=' {
			nonWS++
			continue
		}
		return false
	}
	return nonWS >= 8 && nonWS%4 == 0
}
