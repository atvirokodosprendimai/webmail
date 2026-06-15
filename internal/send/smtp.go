package send

import (
	"crypto/tls"
	"errors"
	"fmt"
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

func Send(cfg Config, from string, to []string, raw []byte) error {
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	switch strings.ToLower(cfg.TLSMode) {
	case "tls":
		return sendTLS(addr, cfg, from, to, raw)
	case "starttls", "":
		return sendSTARTTLS(addr, cfg, from, to, raw)
	case "none":
		return sendPlain(addr, cfg, from, to, raw)
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
	if cfg.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", cfg.Username, cfg.Password, host)); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := c.Mail(extractAddr(from)); err != nil {
		return fmt.Errorf("smtp MAIL FROM: %w", err)
	}
	for _, r := range to {
		if err := c.Rcpt(extractAddr(r)); err != nil {
			return fmt.Errorf("smtp RCPT TO: %w", err)
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
