package send

import (
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/smtp"
	"strings"
	"time"
)

type Config struct {
	Host     string
	Port     int
	TLSMode  string // tls | starttls | none
	Username string
	Password string
}

// Send delivers raw via SMTP. envelopeFrom is what shows up in
// `MAIL FROM:` — should match the auth identity (cfg.Username) for
// most providers; a different header From: in raw is fine. to is the
// list of envelope recipients (To + Cc + Bcc).
func Send(cfg Config, envelopeFrom string, to []string, raw []byte) error {
	if len(to) == 0 {
		return errors.New("send: empty recipient list")
	}
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	switch strings.ToLower(cfg.TLSMode) {
	case "tls":
		return sendTLS(addr, cfg, envelopeFrom, to, raw)
	case "starttls", "":
		return sendSTARTTLS(addr, cfg, envelopeFrom, to, raw)
	case "none":
		return sendPlain(addr, cfg, envelopeFrom, to, raw)
	default:
		return fmt.Errorf("send: unknown TLS mode %q", cfg.TLSMode)
	}
}

func dialSMTP(addr string) (*smtp.Client, error) {
	conn, err := net.DialTimeout("tcp", addr, 15*time.Second)
	if err != nil {
		return nil, err
	}
	host, _, _ := net.SplitHostPort(addr)
	return smtp.NewClient(conn, host)
}

func dialSMTPTLS(addr, host string) (*smtp.Client, error) {
	conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: host})
	if err != nil {
		return nil, err
	}
	return smtp.NewClient(conn, host)
}

func sendTLS(addr string, cfg Config, from string, to []string, raw []byte) error {
	host, _, _ := net.SplitHostPort(addr)
	c, err := dialSMTPTLS(addr, host)
	if err != nil {
		return fmt.Errorf("smtp tls dial: %w", err)
	}
	defer c.Quit()
	return submit(c, host, cfg, from, to, raw)
}

func sendSTARTTLS(addr string, cfg Config, from string, to []string, raw []byte) error {
	host, _, _ := net.SplitHostPort(addr)
	c, err := dialSMTP(addr)
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	defer c.Quit()
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: host}); err != nil {
			return fmt.Errorf("smtp starttls: %w", err)
		}
	} else if strings.ToLower(cfg.TLSMode) == "starttls" {
		return errors.New("smtp: server does not advertise STARTTLS")
	}
	return submit(c, host, cfg, from, to, raw)
}

func sendPlain(addr string, cfg Config, from string, to []string, raw []byte) error {
	host, _, _ := net.SplitHostPort(addr)
	c, err := dialSMTP(addr)
	if err != nil {
		return err
	}
	defer c.Quit()
	return submit(c, host, cfg, from, to, raw)
}

func submit(c *smtp.Client, host string, cfg Config, from string, to []string, raw []byte) error {
	if err := c.Hello("localhost"); err != nil {
		slog.Warn("smtp HELO/EHLO", "err", err)
	}
	if cfg.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", cfg.Username, cfg.Password, host)); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	mailFrom := extractAddr(from)
	if err := c.Mail(mailFrom); err != nil {
		return fmt.Errorf("smtp MAIL FROM <%s>: %w", mailFrom, err)
	}
	for _, r := range to {
		addr := extractAddr(r)
		if err := c.Rcpt(addr); err != nil {
			return fmt.Errorf("smtp RCPT TO <%s>: %w", addr, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp DATA: %w", err)
	}
	if _, err := w.Write(raw); err != nil {
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp data close: %w", err)
	}
	slog.Debug("smtp submit ok", "from", mailFrom, "rcpts", to, "bytes", len(raw))
	return nil
}

// extractAddr pulls the email out of "Name <foo@bar>" or returns the
// input untouched.
func extractAddr(s string) string {
	l := strings.LastIndex(s, "<")
	r := strings.LastIndex(s, ">")
	if l < 0 || r < l {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(s[l+1 : r])
}
