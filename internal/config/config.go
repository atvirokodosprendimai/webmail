// Package config loads environment variables into a typed Config struct.
// Required keys cause boot to fail fast; everything else has a default.
package config

import (
	"fmt"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
)

type Config struct {
	Listen        string        `env:"WEBMAIL_LISTEN" envDefault:":8080"`
	BaseURL       string        `env:"WEBMAIL_BASE_URL" envDefault:"http://localhost:8080"`
	DBPath        string        `env:"WEBMAIL_DB_PATH" envDefault:"./data/webmail.db"`
	UploadsDir    string        `env:"WEBMAIL_UPLOADS_DIR" envDefault:"./data/uploads"`
	SessionKey    string        `env:"WEBMAIL_SESSION_KEY,required"`
	SessionMaxAge time.Duration `env:"WEBMAIL_SESSION_MAX_AGE" envDefault:"720h"`

	IMAPHost          string `env:"IMAP_HOST,required"`
	IMAPPort          int    `env:"IMAP_PORT" envDefault:"993"`
	IMAPTLS           string `env:"IMAP_TLS" envDefault:"tls"`
	IMAPUsername      string `env:"IMAP_USERNAME,required"`
	IMAPPassword      string `env:"IMAP_PASSWORD,required"`
	IMAPSentFolder    string `env:"IMAP_SENT_FOLDER" envDefault:"Sent"`
	IMAPTrashFolder   string `env:"IMAP_TRASH_FOLDER" envDefault:"Trash"`
	IMAPArchiveFolder string `env:"IMAP_ARCHIVE_FOLDER" envDefault:"Archive"`
	IMAPNotesFolder   string `env:"IMAP_NOTES_FOLDER" envDefault:"Notes"`

	SMTPHost     string `env:"SMTP_HOST,required"`
	SMTPPort     int    `env:"SMTP_PORT" envDefault:"587"`
	SMTPTLS      string `env:"SMTP_TLS" envDefault:"starttls"`
	SMTPUsername string `env:"SMTP_USERNAME,required"`
	SMTPPassword string `env:"SMTP_PASSWORD,required"`

	PollInterval       time.Duration `env:"POLL_INTERVAL" envDefault:"60s"`
	FlagSyncEvery      int           `env:"FLAG_SYNC_EVERY" envDefault:"10"`
	AttachmentMaxBytes int64         `env:"ATTACHMENT_MAX_BYTES" envDefault:"26214400"`

	MigrateOnBoot bool `env:"MIGRATE_ON_BOOT" envDefault:"true"`
}

// Load reads .env (if present) and parses env vars into a Config.
func Load() (Config, error) {
	_ = godotenv.Load()
	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	return cfg, nil
}
